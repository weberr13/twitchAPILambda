package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/kukoro"
	"github.com/weberr13/twitchAPILambda/pcg"
)

var (
	ourConfig   *config.Configuration
	channelName string
	channelID   string
)

func init() {
	ourConfig = config.NewConfig()
}

func main() {
	flag.StringVar(&channelName, "channelName", "", "your twitch channel name")
	flag.StringVar(&channelID, "channelID", "", "your twitch channel ID")
	flag.Parse()
	if channelName == "" {
		log.Fatal("please specify channel name to join")
	}
	if channelID == "" {
		log.Fatal("please specify channel id")
	}
	tw, err := chat.NewTwitch(ourConfig)
	if err != nil {
		log.Fatalf("could not reach twitch: %s", err)
	}

	tr, err := ourConfig.GetAuthTokenResponse(channelID, channelName)
	if err == config.ErrNeedAuthorization {
		tw.Close()
		return
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		tw.Close()
		return
	}
	err = tw.SetChatOps()
	if err != nil {
		log.Printf("could not set chat ops: %s", err)
		tw.Close()
		return
	}
auth:
	for {
		log.Printf("attempting to authenticate")
		err = tw.Authenticate("weberr13", tr.Token)
		if err == chat.ErrAuthFailed {
			log.Printf("forcing token reauth")
			tw.Close()
			err = ourConfig.InvalidateToken(channelID, channelName)
			if err != nil {
				log.Printf("could not invalidate old token: %s", err)
				return
			}
			log.Printf("re-fetching auth token")
			tw, err = chat.NewTwitch(ourConfig)
			if err != nil {
				log.Fatalf("could not reach twitch: %s", err)
			}

			tr, err = ourConfig.GetAuthTokenResponse(channelID, channelName)
			if err != nil {
				log.Printf("could not get auth token %s", err)
				tw.Close()
				return
			}
			err = tw.SetChatOps()
			if err != nil {
				log.Printf("could not set chat ops: %s", err)
				tw.Close()
				return
			}
			continue
		}
		log.Printf("authentication successful!")
		break auth
	}
	defer tw.Close()
	err = tw.JoinChannels(channelName)
	if err != nil {
		log.Printf("could not join channel on twitch: %s", err)
		return
	}
readloop:
	for {
		msg, err := tw.ReceiveOneMessage()
		if err == chat.ErrInvalidMsg {
			log.Printf("could not parse message %s: %s", msg.Raw(), err)
			continue
		}
		switch msg.Type() {
		case chat.PrivateMessage:
			switch {
			case pcg.IsSpawnCommand(msg):
				err = pcg.CheckPokemon(channelName, tw)
				if err != nil {
					log.Printf("could not check pokemon %s", msg.Body())
				}
			case msg.User() == "weberr13":
				if kukoro.IsKukoroMsg(msg) {
					fmt.Println("Kukoro says: ", msg.Body())
					continue readloop
				}
			case msg.IsBotCommand():
				switch msg.GetBotCommand() {
				case "kukoro":
					err = tw.SendMessage(channelName, "!getinfo "+msg.DisplayName())
					if err != nil {
						log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
					}
					continue readloop
				case "jump":
					err = tw.SendMessage(channelName, "!getinfo "+msg.DisplayName())
					if err != nil {
						log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
					}
					continue readloop
				case "vote":
					err = tw.SendMessage(channelName, "!getinfo "+msg.GetBotCommandArgs())
					if err != nil {
						log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
					}
					continue readloop
				case "h":
					err = tw.SendMessage(channelName, fmt.Sprintf("%s has voited for another dungeon raid", msg.DisplayName()))
					if err != nil {
						log.Printf("could not send vote info for %s: %s", msg.DisplayName(), err)
					}
					continue readloop
				}

				log.Printf("command: %s, args: %s", msg.GetBotCommand(), msg.GetBotCommandArgs())
			default:
				log.Printf(`%s says: "%s"`, msg.DisplayName(), msg.Body())
			}
		case chat.PingMessage:
			err := tw.Pong(msg)
			if err != nil {
				log.Printf("could not keep connection alive: %s", err)
				return
			}
		case chat.JoinMessage:
			log.Printf("users %v joined the channel", msg.Users())
		case chat.PartMessage:
			log.Printf("users %v left the channel", msg.Users())
		}
	}
	// err = tw.SendMessage(channelName, "!songs current")
	// if err != nil {
	// 	log.Printf("could not send message: %s", err)
	// 	return
	// }

	// log.Printf(`%s says: "%s"`, msg.DisplayName(), msg.Body())
}
