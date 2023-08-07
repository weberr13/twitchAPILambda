package discord

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
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
		resp, err := bc.chat.CreateCompletion(ctx, optionMap["question"].StringValue())
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
		err = bc.SendMessage(i.ChannelID, content)
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
	CreateCompletion(ctx context.Context, message string) (string, error)
}

// BotClient is the bot client struct
type BotClient struct {
	client             *discordgo.Session
	cfg                config.DiscordBotConfig
	replyChan          map[string]struct{}
	chat               AutoChatterer
	registeredCommands []*discordgo.ApplicationCommand
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
		err := bc.SendMessage(channel, message)
		if err != nil {
			log.Printf("could not sent to %s: %s", channel, err)
		}
	}

	return nil
}

// SendMessage sends a simple message to a channel
func (bc *BotClient) SendMessage(channel string, message string) error {
	_, err := bc.client.ChannelMessageSend(channel, message)
	if err != nil {
		return err
	}
	return nil
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
