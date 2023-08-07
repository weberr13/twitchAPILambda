package discord

import (
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/weberr13/twitchAPILambda/config"
)

// BotClient is the bot client struct
type BotClient struct {
	client *discordgo.Session
}

// NewBot makes a bot
func NewBot(conf config.DiscordBotConfig) (*BotClient, error) {
	if conf.Token == "" {
		return nil, fmt.Errorf("cannot connect")
	}
	client, err := discordgo.New("Bot " + conf.Token)
	if err != nil {
		return nil, err
	}
	bc := &BotClient{
		client: client,
	}

	return bc, nil
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
