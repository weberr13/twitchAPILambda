package discord

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/google/uuid"
	"github.com/iancoleman/strcase"
	"github.com/weberr13/twitchAPILambda/autochat"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/db"
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
		{
			Name:        "expgive",
			Description: "give a user experience points",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "user",
					Description: "username to change",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "val",
					Description: "number of points to give the user",
					Required:    true,
				},
			},
		},
		{
			Name:        "expcheck",
			Description: "get a user's level and exp info",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "user",
					Description: "username to change",
					Required:    true,
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
)

func getOptionsMap(commandData discordgo.ApplicationCommandInteractionData) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	options := commandData.Options
	optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
	for _, opt := range options {
		optionMap[opt.Name] = opt
	}
	return optionMap
}

func getRequestor(i *discordgo.InteractionCreate) string {
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
	return user
}

// TODO: make this configurable
var authorizedDMs = map[string]struct{}{
	"weberr13": {}, "xero2772": {},
}

// GiveEXP callback
func (bc *BotClient) GiveEXP(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := getOptionsMap(i.ApplicationCommandData())
	user := getRequestor(i)
	_, ok := authorizedDMs[user]
	if !ok {
		respondToCommand(s, i, "I can't let you do that, Dave")
		log.Printf("user %s is trying to do exp commands, do they need a hammer?", user)
		return
	}
	changeuser, ok := opts["user"]
	if !ok {
		respondToCommand(s, i, "no user specified, what do you want to do?")
		return
	}
	value, ok := opts["val"]
	if !ok {
		respondToCommand(s, i, "no value specified, what do you want?")
		return
	}

	prof := NewUser(i.GuildID, changeuser.StringValue())
	err := bc.persistence.Get(prof.Key(), prof)
	switch err {
	case db.ErrNotFound:
		fallthrough
	case nil:
		prof.AddExp(int(value.IntValue()))
		err = bc.persistence.Put(prof.Key(), prof)
		if err != nil {
			respondToCommand(s, i, fmt.Sprintf("something went terribly wrong, send this to weberr13 %s", err))
			return
		}
		respondToCommand(s, i, fmt.Sprintf("user %s is level %d with %d exp", prof.Name, prof.Level(), prof.CurrentExp))
	default:
		respondToCommand(s, i, fmt.Sprintf("something went terribly wrong, send this to weberr13 %s", err))
		return
	}
}

// CheckUserEXP callback
func (bc *BotClient) CheckUserEXP(s *discordgo.Session, i *discordgo.InteractionCreate) {
	opts := getOptionsMap(i.ApplicationCommandData())
	user := getRequestor(i)
	changeuser, ok := opts["user"]
	if !ok {
		respondToCommand(s, i, "no user specified, what do you want to do?")
		return
	}
	_, ok = authorizedDMs[user]
	if !ok && user != changeuser.Name {
		respondToCommand(s, i, "I can't let you do that, Dave")
		log.Printf("user %s is trying spy on other people, do they need a hammer?", user)
		return
	}
	prof := NewUser(i.GuildID, changeuser.StringValue())
	err := bc.persistence.Get(prof.Key(), prof)
	switch err {
	case db.ErrNotFound:
		err = bc.persistence.Put(prof.Key(), prof)
		if err != nil {
			respondToCommand(s, i, fmt.Sprintf("something went terribly wrong, send this to weberr13 %s", err))
			return
		}
		fallthrough
	case nil:
		respondToCommand(s, i, fmt.Sprintf("user %s is level %d with %d exp", prof.Name, prof.Level(), prof.CurrentExp))
		return
	default:
		respondToCommand(s, i, fmt.Sprintf("something went terribly wrong, send this to weberr13 %s", err))
		return
	}
}

func respondToCommand(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
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

// AskCommand uses openai
func (bc *BotClient) AskCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	respC := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		optionMap := getOptionsMap(i.ApplicationCommandData())
		user := getRequestor(i)

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
		respondToCommand(s, i, "this is taking a while, I'll get back to you on that")
		content := <-respC
		_, err := bc.SendMessage(i.ChannelID, content)
		if err != nil {
			log.Printf("failed to send message: %s", err)
		}
	case content := <-respC:
		respondToCommand(s, i, content)
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
	token              string
	persistence        db.Persister
	sync.RWMutex
}

// NewBot makes a bot
func NewBot(conf config.DiscordBotConfig, autochater AutoChatterer, persister db.Persister) (*BotClient, error) {
	if conf.Token == "" {
		return nil, fmt.Errorf("cannot connect")
	}

	bc := &BotClient{
		token:       conf.Token,
		cfg:         conf,
		chat:        autochater,
		replyChan:   map[string]struct{}{},
		persistence: persister,
	}
	for _, ch := range bc.cfg.ReplyChannels {
		bc.replyChan[ch] = struct{}{}
	}
	commandHandlers["ask"] = bc.AskCommand
	commandHandlers["expgive"] = bc.GiveEXP
	commandHandlers["expcheck"] = bc.CheckUserEXP

	err := bc.Open()
	if err != nil {
		return nil, err
	}
	return bc, nil
}

// Open the connection to discord
func (bc *BotClient) Open() error {
	client, err := discordgo.New("Bot " + bc.token)
	if err != nil {
		return err
	}
	client.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		} else {
			log.Printf("unknownd command %s", i.ApplicationCommandData().Name)
		}
	})
	err = client.Open()
	if err != nil {
		log.Printf("could not connect to discord: %s", err)
		return err
	}
	log.Println("Adding commands to discord...")
	bc.registeredCommands = make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		log.Printf("registereing %s", v.Name)
		cmd, err := client.ApplicationCommandCreate(client.State.User.ID, GuildID, v)
		if err != nil {
			return fmt.Errorf("Cannot create '%v' command: %w", v.Name, err)
		}
		bc.registeredCommands[i] = cmd
		log.Printf("registered %#v", cmd)
	}
	bc.client = client
	return nil
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
func (bc *BotClient) sendShoutoutToChannelForUsers(ctx context.Context, knownUsers map[string]*discordgo.Message, users []string, channel string, streams map[string]StreamInfo, description string) {
	interestedUsers := map[string]struct{}{}
	for _, k := range users {
		interestedUsers[strings.ToLower(k)] = struct{}{}
	}
	allUsers := []string{}
	for k := range streams {
		if _, ok := interestedUsers[strings.ToLower(k)]; ok {
			allUsers = append(allUsers, k)
		}
	}
	if len(allUsers) > 0 {
		log.Printf("live streams of interest are %#v", allUsers)
	}
	if len(knownUsers) > 0 {
		log.Printf("live streams of interest wee already know about are %#v", knownUsers)
	}
	for user, msg := range knownUsers {
		user = strings.ToLower(user)
		if ctx.Err() != nil {
			log.Printf("timed out updating known user")
			return
		}
		if msg == nil {
			delete(knownUsers, user)
			continue
		}
		if sinfo, ok := streams[strings.TrimSuffix(user, channel)]; ok {
			if sinfo.Type != "live" {
				log.Printf("stream no longer live: remove message with ID: %s in Channel %s, in Guild %s", msg.ID, msg.ChannelID, msg.GuildID)
				err := bc.DeleteMessage(msg.ChannelID, msg.ID)
				if err != nil {
					log.Printf("could not remove our golive message %s", err)
				}
				delete(knownUsers, user)
			} else {
				log.Printf("updating with the new thumbnail: %s", sinfo.ThumbnailURL)
				info := GoLiveInfo{
					User:        sinfo.UserName,
					Viewers:     sinfo.ViewerCount,
					Platform:    "Twitch",
					Game:        sinfo.GameName,
					Description: description,
				}
				msg, err := bc.UpdateGoLiveMessage(msg,
					sinfo.ThumbnailURL, fmt.Sprintf("https://twitch.tv/%s", sinfo.UserLogin), info)
				if err != nil {
					log.Printf("could not send msg: %s", err)
					delete(knownUsers, user)
				} else {
					knownUsers[user] = msg
				}
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
		user = strings.ToLower(user)
		if ctx.Err() != nil {
			log.Printf("timed out updating new users")
			return
		}
		_, ok := interestedUsers[user]
		if !ok {
			continue
		}
		if sinfo.Type == "live" {
			log.Printf("%s is live", user)
			if _, ok := knownUsers[user]; !ok {
				log.Printf("sending I'm live for %s %s", user, channel)
				info := GoLiveInfo{
					User:        sinfo.UserName,
					Viewers:     sinfo.ViewerCount,
					Platform:    "Twitch",
					Game:        sinfo.GameName,
					Description: description,
				}
				msg, err := bc.SendGoLIveMessage(channel,
					sinfo.ThumbnailURL, fmt.Sprintf("https://twitch.tv/%s", sinfo.UserLogin), info)
				if err != nil {
					log.Printf("could not send msg: %s", err)
					delete(knownUsers, user)
					continue
				}
				knownUsers[user] = msg
			} else {
				log.Printf("not sending new message, one already exists %v", knownUsers[user])
			}
		}
	}
}

// ImLive discord message info
type ImLive struct {
	Channel string
	User    string
	Message *discordgo.Message
}

var knownUserPrefix = "imlive-"

// Key for persistence
func (i ImLive) Key() string {
	return fmt.Sprintf("%s%s-%s", knownUserPrefix, i.Channel, i.User)
}

func getKnowUsersMessages(persister db.Persister) map[string]map[string]*discordgo.Message {
	m := map[string]map[string]*discordgo.Message{}
	keys, err := persister.PrefixScan(knownUserPrefix)
	if err != nil {
		log.Printf("could not read known users in I'm live, starting over: %s", err)
		return m
	}
	for _, key := range keys {
		im := &ImLive{}
		err = persister.Get(key, im)
		if err != nil {
			log.Printf("could not read known user %s in I'm live, skipping: %s", key, err)
			continue
		}
		_, ok := m[im.Channel]
		if !ok {
			m[im.Channel] = make(map[string]*discordgo.Message)
		}
		m[im.Channel][strings.ToLower(im.User)] = im.Message
	}
	return m
}

func saveKnowUsersMessages(m map[string]map[string]*discordgo.Message, persister db.Persister) {
	for ch, msgs := range m {
		for user, msg := range msgs {
			im := &ImLive{
				Channel: ch,
				User:    user,
				Message: msg,
			}
			err := persister.Put(im.Key(), im)
			if err != nil {
				log.Printf("could not save known user %v in I'm live %s", im, err)
			}
		}
	}
	_ = persister.Sync()
}

// RunAutoShoutouts will start an asyncronous runner that manages shoutouts
func (bc *BotClient) RunAutoShoutouts(ctx context.Context, wg *sync.WaitGroup, chanToUsers map[string]config.GoLiveConfig, getLiveF GetLiveWrapper, persister db.Persister) {
	wg.Add(1)
	go func() {
		knownUsers := getKnowUsersMessages(persister)
		allUserSet := map[string]struct{}{}
		for _, users := range chanToUsers {
			for _, user := range users.TwitchUsers {
				allUserSet[strings.ToLower(user)] = struct{}{}
			}
		}
		allUsers := []string{}
		for user := range allUserSet {
			allUsers = append(allUsers, user)
		}
		var channelInfo map[string]StreamInfo
		var err error
		for {
			channelInfo, err = getLiveF(allUsers)
			if err != nil {
				log.Printf("could not get live status, waiting %s", err)
				time.Sleep(60 * time.Second)
				continue
			}
			break
		}

		for channel, users := range chanToUsers {
			if _, ok := knownUsers[channel]; !ok {
				knownUsers[channel] = make(map[string]*discordgo.Message)
			}
			log.Printf("sending shoutouts for channel %s %v of %v, known %v", channel, users.TwitchUsers, channelInfo, knownUsers[channel])
			bc.sendShoutoutToChannelForUsers(ctx, knownUsers[channel], users.TwitchUsers, channel, channelInfo, users.LiveMessage)
		}
		saveKnowUsersMessages(knownUsers, persister)
		defer wg.Done()
		timer := time.NewTicker(60 * time.Second) // TODO: configurable?
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				// clean up?
				log.Printf("shutting down")
				return
			case <-timer.C:
				for {
					channelInfo, err = getLiveF(allUsers)
					if err != nil {
						log.Printf("could not get live status, waiting %s", err)
						time.Sleep(60 * time.Second)
						continue
					}
					break
				}
				for channel, users := range chanToUsers {
					if _, ok := knownUsers[channel]; !ok {
						knownUsers[channel] = make(map[string]*discordgo.Message)
					}
					log.Printf("sending shoutouts for channel %s %v of %v", channel, users.TwitchUsers, channelInfo)
					bc.sendShoutoutToChannelForUsers(ctx, knownUsers[channel], users.TwitchUsers, channel, channelInfo, users.LiveMessage)
				}
				saveKnowUsersMessages(knownUsers, persister)
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
			log.Printf("removing %#v", v)
			err := bc.client.ApplicationCommandDelete(bc.client.State.User.ID, GuildID, v.ID)
			if err != nil {
				log.Printf("Cannot delete '%v' command: %v", v.Name, err)
			}
		}
	}
	// Cleanly close down the Discord session.
	log.Printf("Closing discord client")
	err := bc.client.Close()
	bc.client = nil
	log.Printf("discord client closed")
	return err
}

// BroadcastMessage sends a simple message
func (bc *BotClient) BroadcastMessage(channels []string, message string) ([]*discordgo.Message, error) {
	msgs := []*discordgo.Message{}
	for _, channel := range channels {
		msg, err := bc.SendMessage(channel, message)
		if err != nil {
			log.Printf("could not sent to %s: %s", channel, err)
			continue
		}
		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// SendMessage sends a simple message to a channel
func (bc *BotClient) SendMessage(channel string, message string) (*discordgo.Message, error) {
	bc.Lock()
	defer bc.Unlock()
	return bc.client.ChannelMessageSend(channel, message)
}

// SendPokemonMessage sends a temporary discord message for a pokemon spawn
func (bc *BotClient) SendPokemonMessage(msg string, channelName string, pcgChannels []string) {
	// OhMyDog A wild Snubbull appears OhMyDog Catch it using !pokecatch (winners revealed in 90s)
	// TwitchLit A wild Yamper appears TwitchLit Catch it using !pokecatch (winners revealed in 90s)
	i := strings.Index(msg, "A wild ")
	j := strings.Index(msg, " appears")
	k := strings.Index(msg, " appears TwitchLit Catch")
	specialEvent := false
	if k == -1 {
		specialEvent = true
	}
	if i > 0 && j > i {
		msg = msg[i+len("A wild ") : j]
	}

	pokename := msg
	urlName := regexp.MustCompile(`[^a-z0-9 ]+`).ReplaceAllString(strings.ToLower(msg), "")
	urlName = strings.ReplaceAll(urlName, " ", "-")

	for _, ch := range pcgChannels {
		var msgText string
		if specialEvent {
			msgText = fmt.Sprintf("A **special event pokemon** [%s](https://www.pokemon.com/us/pokedex/%s) has spawned in %s, go to https://twitch.tv/%s to catch it", pokename, urlName, channelName, channelName)
		} else {
			msgText = fmt.Sprintf("A wild [%s](https://www.pokemon.com/us/pokedex/%s) has spawned in %s, go to https://twitch.tv/%s to catch it", pokename, urlName, channelName, channelName)
		}
		msg, err := bc.SendMessage(ch, msgText)
		if err != nil {
			log.Printf("could not post pokemon spawn: %s", err)
		} else {
			go func(chanID, msgID string) {
				time.Sleep(90 * time.Second)
				err := bc.DeleteMessage(chanID, msgID)
				if err != nil {
					log.Printf("could not delete spawn message: %s", err)
				}
			}(msg.ChannelID, msg.ID)
		}
	}
}

// UpdateMessage that we sent
func (bc *BotClient) UpdateMessage(old *discordgo.Message, text string) (*discordgo.Message, error) {
	if old == nil {
		return nil, fmt.Errorf("cannot update an empty message")
	}
	msgEdit := &discordgo.MessageEdit{
		Content:    &text,
		Embeds:     old.Embeds,
		Components: old.Components,
	}
	msgEdit.Flags = old.Flags
	msgEdit.Attachments = &old.Attachments
	msgEdit.ID = old.ID
	msgEdit.Channel = old.ChannelID

	bc.Lock()
	defer bc.Unlock()
	if bc.client == nil {
		return nil, fmt.Errorf("no client found")
	}
	st, err := bc.client.ChannelMessageEditComplex(msgEdit)
	if err != nil {
		log.Printf("failure to send channel message %s", err)
	}
	return st, err
}

// UpdateGoLiveMessage update a golive with new info
func (bc *BotClient) UpdateGoLiveMessage(old *discordgo.Message, thumbnail, url string, info GoLiveInfo) (*discordgo.Message, error) {
	if old == nil {
		return nil, fmt.Errorf("cannot update an empty message")
	}

	msg := bc.formatGoLive(thumbnail, url, info)

	msgEdit := &discordgo.MessageEdit{
		Content:         &msg.Content,
		Embeds:          msg.Embeds,
		Components:      msg.Components,
		AllowedMentions: msg.AllowedMentions,
		Files:           msg.Files,
		Embed:           msg.Embed,
	}
	msgEdit.Flags = old.Flags
	msgEdit.Attachments = &old.Attachments
	msgEdit.ID = old.ID
	msgEdit.Channel = old.ChannelID

	bc.Lock()
	defer bc.Unlock()
	if bc.client == nil {
		return nil, fmt.Errorf("no client found")
	}
	st, err := bc.client.ChannelMessageEditComplex(msgEdit)
	if err != nil {
		log.Printf("failure to send channel message %s", err)
	}
	return st, err
}

// GoLiveInfo allows for text formatting
type GoLiveInfo struct {
	User        string
	Viewers     int
	Platform    string
	Description string
	Game        string
}

func (bc *BotClient) formatGoLive(thumbnail, url string, info GoLiveInfo) *discordgo.MessageSend {
	info.User = strcase.ToCamel(info.User)
	var description string
	if info.Description == "" {
		description = fmt.Sprintf("%s is live playing with %d viewers", info.User, info.Viewers)
	} else {
		tmpl, err := template.New("description").Parse(info.Description)
		if err != nil {
			description = fmt.Sprintf("%s is live playing with %d viewers", info.User, info.Viewers)
		} else {
			var tpl bytes.Buffer
			err = tmpl.Execute(&tpl, info)
			if err != nil {
				description = fmt.Sprintf("%s is live playing with %d viewers", info.User, info.Viewers)
			} else {
				description = tpl.String()
			}
		}
	}

	height := 108 * 2
	width := 192 * 2
	embeds := []*discordgo.MessageEmbed{}
	imageURL := strings.ReplaceAll(strings.ReplaceAll(thumbnail, "{width}", fmt.Sprintf("%d", width)), "{height}", fmt.Sprintf("%d", height))
	imageURL += "?" + uuid.Must(uuid.NewRandom()).String()
	embeds = append(embeds, &discordgo.MessageEmbed{
		Title:       info.Platform,
		Description: description,
		Footer: &discordgo.MessageEmbedFooter{
			Text: "brought to you by @weberr13",
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
				Value: info.Game,
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
						Emoji: discordgo.ComponentEmoji{
							Name: "👀",
						},
					},
				},
			},
		},
		Embeds: embeds,
	}
	return msg
}

// SendGoLIveMessage send a message with embedds included
func (bc *BotClient) SendGoLIveMessage(channel string, thumbnail, url string, info GoLiveInfo) (*discordgo.Message, error) {
	msg := bc.formatGoLive(thumbnail, url, info)
	bc.Lock()
	defer bc.Unlock()
	if bc.client == nil {
		return nil, fmt.Errorf("no connection")
	}
	st, err := bc.client.ChannelMessageSendComplex(channel, msg)
	if err != nil {
		log.Printf("failure to send channel message %#v, %s", msg, err)
		// err = bc.Close()
		// if err != nil {
		// 	log.Panicf("tried to reconnect, close failed: %s", err)
		// 	return nil, err
		// }
		// err = bc.Open()
		// if err != nil {
		// 	log.Panicf("tried to reconnect, open failed: %s", err)
		// 	return nil, err
		// }
		// st, err = bc.client.ChannelMessageSendComplex(channel, msg)
	}
	return st, err
}

// DeleteMessage deletes a message that we sent
func (bc *BotClient) DeleteMessage(channelID, messageID string) error {
	bc.Lock()
	defer bc.Unlock()
	if bc.client == nil {
		return fmt.Errorf("no connection")
	}
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
