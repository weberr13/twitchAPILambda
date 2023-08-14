package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/weberr13/twitchAPILambda/autochat"
	"github.com/weberr13/twitchAPILambda/config"
)

var (
	// RemoveCommands on exit
	RemoveCommands = true // TODO: make this a config
	// GuildID for a given guild or globally if empty
	GuildID  = "" // TODO: make the guild a config
	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "ask",
			Description: "Ask me something",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "question",
					Description: "ask text",
					Required:    true,
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
)

// AskCommand uses openai
func (bc *BotClient) AskCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	respC := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		options := i.ApplicationCommandData().Options
		// Or convert the slice into a map
		optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
		for _, opt := range options {
			optionMap[opt.Name] = opt
		}
		user := "default"
		if i.User != nil {
			user = i.User.Username
		} else if i.Member != nil {
			if i.Member.User != nil {
				user = i.Member.User.Username
			} else if i.Member.Nick != "" {
				user = i.Member.Nick
			}
		}
		log.Printf("determined user of requestor is %s", user)

		resp, err := bc.chat.CreateCompletion(ctx, optionMap["question"].StringValue(), autochat.WithRateLimit(user, 5*time.Minute))
		if err != nil {
			log.Printf("openai failed: %s", err)
			respC <- "I cannot answer that right now, Dave"
			return
		}
		content := fmt.Sprintf(`The oracle has concluded that the answer to "%s" is :%s`, optionMap["question"].StringValue(), resp)
		respC <- content
	}()
	select {
	case <-time.After(2500 * time.Millisecond):
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "this is taking a while, I'll get back to you on that",
			},
		})
		if err != nil {
			log.Printf("failed to send message: %s", err)
		}
		content := <-respC
		_, err = bc.SendMessage(i.ChannelID, content)
		if err != nil {
			log.Printf("failed to send message: %s", err)
		}
	case content := <-respC:
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: content,
			},
		})
		if err != nil {
			log.Printf("failed to send message: %s", err)
		}
	}
}

// AutoChatterer can do chat stuff
type AutoChatterer interface {
	CreateCompletion(ctx context.Context, message string, opts ...autochat.CompletionOpt) (string, error)
}

// BotClient is the bot client struct
type BotClient struct {
	client             *discordgo.Session
	cfg                config.DiscordBotConfig
	replyChan          map[string]struct{}
	chat               AutoChatterer
	registeredCommands []*discordgo.ApplicationCommand
	sync.RWMutex
}

// NewBot makes a bot
func NewBot(conf config.DiscordBotConfig, autochater AutoChatterer) (*BotClient, error) {
	if conf.Token == "" {
		return nil, fmt.Errorf("cannot connect")
	}
	client, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}
	bc := &BotClient{
		client:    client,
		cfg:       conf,
		chat:      autochater,
		replyChan: map[string]struct{}{},
	}
	for _, ch := range bc.cfg.ReplyChannels {
		bc.replyChan[ch] = struct{}{}
	}
	commandHandlers["ask"] = bc.AskCommand
	bc.client.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		} else {
			log.Printf("unknownd command %s", i.ApplicationCommandData().Name)
		}
	})
	err = bc.client.Open()
	if err != nil {
		log.Printf("could not connect to discord: %s", err)
		return nil, err
	}
	log.Println("Adding commands to discord...")
	bc.registeredCommands = make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		log.Printf("registereing %s", v.Name)
		cmd, err := bc.client.ApplicationCommandCreate(bc.client.State.User.ID, GuildID, v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		bc.registeredCommands[i] = cmd
		log.Printf("registered %#v", cmd)
	}
	return bc, nil
}

// StreamInfo needed for go-live notifs
type StreamInfo struct {
	UserLogin    string    `json:"user_login"`
	UserName     string    `json:"user_name"`
	GameName     string    `json:"game_name"`
	Type         string    `json:"type"` // "live"
	Title        string    `json:"title"`
	ViewerCount  int       `json:"viewer_count"`
	StartedAt    time.Time `json:"started_at"`
	Language     string    `json:"language"`
	ThumbnailURL string    `json:"thumbnail_url"`
	IsMature     bool      `json:"is_mature"`
}

// GetLiveWrapper is a function that returns stream info normalized for what this package requires
type GetLiveWrapper func([]string) (map[string]StreamInfo, error)

// SINGLE THREADED!
func (bc *BotClient) sendShoutoutToChannelForUsers(knownUsers map[string]*discordgo.Message, users []string, channel string, getLiveF GetLiveWrapper) {
	streams, err := getLiveF(users)
	if err != nil {
		log.Printf("could not get live streams: %s", err)
		return
	}
	log.Printf("live streams of interest are %#v", streams)
	for user, msg := range knownUsers {
		if sinfo, ok := streams[user]; ok {
			if sinfo.Type != "live" {
				log.Printf("stream no longer live: remove message with ID: %s in Channel %s, in Guild %s", msg.ID, msg.ChannelID, msg.GuildID)
				err := bc.DeleteMessage(msg.ChannelID, msg.ID)
				if err != nil {
					log.Printf("could not remove our golive message %s", err)
				}
				delete(knownUsers, user)
			} else {
				log.Printf("updating with the new thumbnail: %s", sinfo.ThumbnailURL)
				msg, err := bc.UpdateGoLiveMessage(msg,
					fmt.Sprintf(`%s is live playing with %d viewers`, sinfo.UserName, sinfo.ViewerCount),
					sinfo.ThumbnailURL, fmt.Sprintf("https://twitch.tv/%s", sinfo.UserLogin), sinfo.GameName)
				if err != nil {
					log.Printf("could not send msg: %s", err)
				}
				knownUsers[user] = msg
			}
		} else {
			// remove go live message
			log.Printf("can't find stream: remove message with ID: %s in Channel %s, in Guild %s", msg.ID, msg.ChannelID, msg.GuildID)
			err := bc.DeleteMessage(msg.ChannelID, msg.ID)
			if err != nil {
				log.Printf("could not remove our golive message %s", err)
			}
			delete(knownUsers, user)
		}
	}
	for user, sinfo := range streams {
		if _, ok := knownUsers[user]; !ok && sinfo.Type == "live" {
			msg, err := bc.SendGoLIveMessage(channel,
				fmt.Sprintf(`%s is live playing with %d viewers`, sinfo.UserName, sinfo.ViewerCount),
				sinfo.ThumbnailURL, fmt.Sprintf("https://twitch.tv/%s", sinfo.UserLogin), sinfo.GameName)
			if err != nil {
				log.Printf("could not send msg: %s", err)
				return
			}
			knownUsers[user] = msg
		}
	}
}

// RunAutoShoutouts will start an asyncronous runner that manages shoutouts
func (bc *BotClient) RunAutoShoutouts(ctx context.Context, wg *sync.WaitGroup, chanToUsers map[string][]string, getLiveF GetLiveWrapper) {
	wg.Add(1)
	go func() {
		knownUsers := map[string]map[string]*discordgo.Message{}
		defer wg.Done()
		timer := time.NewTicker(60 * time.Second) // TODO: configurable?
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				// clean up?
				return
			case <-timer.C:
				for channel, users := range chanToUsers {
					if _, ok := knownUsers[channel]; !ok {
						knownUsers[channel] = make(map[string]*discordgo.Message)
					}
					bc.sendShoutoutToChannelForUsers(knownUsers[channel], users, channel, getLiveF)
				}
			}
		}
	}()
}

// Close down the backend connection cleanly
func (bc *BotClient) Close() error {
	if bc.client == nil {
		return nil
	}
	if RemoveCommands {
		log.Println("Removing commands...")
		// // We need to fetch the commands, since deleting requires the command ID.
		// // We are doing this from the returned commands on line 375, because using
		// // this will delete all the commands, which might not be desirable, so we
		// // are deleting only the commands that we added.
		// registeredCommands, err := s.ApplicationCommands(s.State.User.ID, *GuildID)
		// if err != nil {
		// 	log.Fatalf("Could not fetch registered commands: %v", err)
		// }

		for _, v := range bc.registeredCommands {
			err := bc.client.ApplicationCommandDelete(bc.client.State.User.ID, GuildID, v.ID)
			if err != nil {
				log.Panicf("Cannot delete '%v' command: %v", v.Name, err)
			}
		}
	}
	// Cleanly close down the Discord session.
	err := bc.client.Close()
	bc.client = nil
	return err
}

// BroadcastMessage sends a simple message
func (bc *BotClient) BroadcastMessage(channels []string, message string) error {
	for _, channel := range channels {
		_, err := bc.SendMessage(channel, message)
		if err != nil {
			log.Printf("could not sent to %s: %s", channel, err)
		}
	}

	return nil
}

// SendMessage sends a simple message to a channel
func (bc *BotClient) SendMessage(channel string, message string) (*discordgo.Message, error) {
	bc.Lock()
	defer bc.Unlock()
	return bc.client.ChannelMessageSend(channel, message)
}

// UpdateGoLiveMessage update a golive with new info
func (bc *BotClient) UpdateGoLiveMessage(old *discordgo.Message, title, thumbnail, url, game string) (*discordgo.Message, error) {
	msg := bc.formatGoLive(title, thumbnail, url, game)

	msgEdit := &discordgo.MessageEdit{
		Content:         &msg.Content,
		Embeds:          msg.Embeds,
		Components:      msg.Components,
		Flags:           old.Flags,
		AllowedMentions: msg.AllowedMentions,
		Files:           msg.Files,
		Attachments:     &old.Attachments,
		Embed:           msg.Embed,
		ID:              old.ID,
		Channel:         old.ChannelID,
	}
	bc.Lock()
	defer bc.Unlock()
	return bc.client.ChannelMessageEditComplex(msgEdit)
}

func (bc *BotClient) formatGoLive(title, thumbnail, url, game string) *discordgo.MessageSend {
	height := 108 * 2
	width := 192 * 2
	embeds := []*discordgo.MessageEmbed{}
	imageURL := strings.ReplaceAll(strings.ReplaceAll(thumbnail, "{width}", fmt.Sprintf("%d", width)), "{height}", fmt.Sprintf("%d", height))
	imageURL += "?" + uuid.Must(uuid.NewRandom()).String()
	embeds = append(embeds, &discordgo.MessageEmbed{
		Title:       "Twtich",
		Description: title,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "brought to you by xlgbot @weberr13",
		},
		URL: url,
		Image: &discordgo.MessageEmbedImage{
			URL:    imageURL,
			Width:  width,
			Height: height,
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Game",
				Value: game,
			},
		},
	})
	msg := &discordgo.MessageSend{
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					&discordgo.Button{
						Style: discordgo.LinkButton,
						Label: "Watch Now",
						URL:   url,
					},
				},
			},
		},
		Embeds: embeds,
	}
	return msg
}

// SendGoLIveMessage send a message with embedds included
func (bc *BotClient) SendGoLIveMessage(channel string, title, thumbnail, url, game string) (*discordgo.Message, error) {
	msg := bc.formatGoLive(title, thumbnail, url, game)
	bc.Lock()
	defer bc.Unlock()
	return bc.client.ChannelMessageSendComplex(channel, msg)
}

// DeleteMessage deletes a message that we sent
func (bc *BotClient) DeleteMessage(channelID, messageID string) error {
	bc.Lock()
	defer bc.Unlock()
	return bc.client.ChannelMessageDelete(channelID, messageID)
}

// // This function will be called (due to AddHandler above) every time a new
// // message is created on any channel that the authenticated bot has access to.
// func (bc *BotClient) messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
// 	log.Printf("got discord message %#v", m)
// 	// Ignore all messages created by the bot itself
// 	// This isn't required in this specific example but it's a good practice.
// 	if m.Author.ID == s.State.User.ID {
// 		log.Printf("author is us?")
// 		return
// 	}
// 	// Ignore messages that are in channels we are not configured to reply to
// 	if _, ok := bc.replyChan[m.ChannelID]; !ok {
// 		log.Printf("this is a channel we should not be watching?")
// 		return
// 	}

// 	msgs, err := bc.client.ChannelMessages(m.ChannelID, 1, "", "", m.ID)
// 	if err != nil {
// 		fmt.Printf("could not find messages: %s", err)
// 		return
// 	}
// 	for _, msg := range msgs {
// 		log.Printf(`found: "%v"`, msg.Content)
// 		cmd := strings.SplitN(m.Content, " ", 1)
// 		switch cmd[0] {
// 		case "!ask":
// 			func() {
// 				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
// 				defer cancel()
// 				resp, err := bc.chat.CreateCompletion(ctx, cmd[1])
// 				if err != nil {
// 					log.Printf("openai failed: %s", err)
// 					_, err = s.ChannelMessageSend(m.ChannelID, "I cannot answer that right now, Dave")
// 					if err != nil {
// 						log.Printf("failed to send message: %s", err)
// 					}
// 					return
// 				}
// 				_, err = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("TThe oracle has concluded that: %s", resp))
// 				if err != nil {
// 					log.Printf("failed to send message: %s", err)
// 				}
// 			}()

// 		default:
// 			log.Printf("we got %s", m.Content)
// 			return
// 		}
// 	}
// }
