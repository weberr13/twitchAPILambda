package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/weberr13/twitchAPILambda/autochat"
	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/discord"
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
	if ourConfig.Twitch.ChannelName != "" {
		channelName = ourConfig.Twitch.ChannelName
	}
	if ourConfig.Twitch.ChannelID != "" {
		channelID = ourConfig.Twitch.ChannelID
	}
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
	autoChatter := autochat.NewOpenAI(ourConfig.OpenAIKey)
	discordBot, err := discord.NewBot(*ourConfig.Discord, autoChatter)
	if err != nil {
		log.Printf("not starting discord bot: %s", err)
	} else {
		defer discordBot.Close()
		err := discordBot.BroadcastMessage(ourConfig.Discord.LogChannels, fmt.Sprintf("xlg discord bot has started for %s", channelName))
		if err != nil {
			log.Printf("could not send discord test message: %s", err)
		}
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
		log.Printf("authentication successful!!!")
		break auth
	}
	defer tw.Close()
	err = tw.JoinChannels(channelName)
	if err != nil {
		log.Printf("could not join channel on twitch: %s", err)
		return
	}
	knownusers := map[string]string{}
	err = tw.SendMessage(channelName, "xlg bot has joined")
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
		log.Printf("got %s", msg.String())
		switch msg.Type() {
		case chat.PrivateMessage:
			switch {
			case pcg.IsRegistered(msg):
				if !pcg.IsCaught(msg) {
					user := pcg.IsCaughtUser(msg)
					if user == "weberr13" { // the bot runs as me
						err = pcg.CatchPokemon(channelName, tw, "ultraball")
						if err != nil {
							log.Printf("could not auto catch")
						}
					}
				}
			case pcg.IsSpawnCommand(msg):
				err = pcg.CheckPokemon(channelName, tw)
				if err != nil {
					log.Printf("could not check pokemon %s", msg.Body())
				}
				if discordBot != nil {
					err := discordBot.BroadcastMessage(ourConfig.Discord.BroadcastChannels, fmt.Sprintf("a pokemon has spawned in %s, go to https://twitch.tv/%s to catch it: %s", channelName, channelName, msg.Body()))
					if err != nil {
						log.Printf("could not post pokemon spawn: %s", err)
					}
				}
			case msg.User() == "weberr13":
				if kukoro.IsKukoroMsg(msg) {
					fmt.Println("Kukoro says: ", msg.Body())
					continue readloop
				}
			case msg.IsBotCommand():
				switch msg.GetBotCommand() {
				case "ask":
					if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
						func() {
							_ = tw.SendMessage(channelName, "The oracle has heard your question, please wait...")
							ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
							defer cancel()
							resp, err := autoChatter.CreateCompletion(ctx, msg.GetBotCommandArgs())
							if err != nil {
								log.Printf("openai failed: %s", err)
								_ = tw.SendMessage(channelName, "I cannot answer that right now, Dave")
								return
							}
							preamble := "The oracle has concluded that: "
							if len(resp) > chat.TwitchCharacterLimit-len(preamble) {
								for len(resp) > chat.TwitchCharacterLimit-len(preamble) {
									err = tw.SendMessage(channelName, fmt.Sprintf("%s%s", preamble, resp[0:chat.TwitchCharacterLimit-len(preamble)]))
									if err != nil {
										log.Printf("failed to send chat response: %s", err)
										_ = tw.SendMessage(channelName, "Something has gone terribly wrong, check logs for details")
										return
									}
									resp = resp[chat.TwitchCharacterLimit-len(preamble):]
									preamble = "cont: "
								}
							}
							err = tw.SendMessage(channelName, fmt.Sprintf("%s%s", preamble, resp))
							if err != nil {
								log.Printf("failed to send chat response: %s", err)
								_ = tw.SendMessage(channelName, "Something has gone terribly wrong, check logs for details")
								return
							}
						}()
					} else {
						log.Printf("got ask command from %s", msg.GoString())
					}
					continue readloop
				case "bye":
					if msg.IsMod() {
						tw.Farewell(channelName, msg.GetBotCommandArgs())
					} else {
						log.Printf("got bye command from %s", msg.GoString())
					}
					continue readloop
				case "so":
					if msg.IsMod() {
						tw.Shoutout(channelName, msg.GetBotCommandArgs(), true)
					} else {
						log.Printf("got so command from %s", msg.GoString())
					}
					continue readloop
				case "raidmsg":
					if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
						// TODO: put this in config?
						err = tw.SendMessage(channelName, "Weberr13 RAID weberrMioRaid weberrMioRaid weberrMioRaid")
						if err != nil {
							log.Printf("could not send raid message %s: %s", msg.DisplayName(), err)
						}
					}
					continue readloop
				case "subraid":
					if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
						err = tw.SendMessage(channelName, "Weberr13 RAID weberrMioRaid weberrMioCheer weberrMioRaid")
						if err != nil {
							log.Printf("could not send raid message %s: %s", msg.DisplayName(), err)
						}
					}
					continue readloop
				case "whois":
					if msg.IsMod() {
						users := []string{}
						for k := range knownusers {
							users = append(users, k)
						}
						err = tw.SendMessage(channelName, fmt.Sprintf("Current users are: %v", users))
						if err != nil {
							log.Printf("could not send whgois %s: %s", msg.DisplayName(), err)
						}
					}
					continue readloop
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
			shoutouts := map[string]string{}
			for k, v := range msg.Users() {
				if _, ok := knownusers[k]; !ok {
					shoutouts[k] = v
				} else {
					log.Printf("existing user: %s:%s", k, v)
				}
				knownusers[k] = v
			}
			chat.TrimBots(knownusers)
			chat.TrimBots(shoutouts)
			for k, v := range shoutouts {
				log.Printf("new user %s:%s joined", k, v)
				tw.Shoutout(channelName, k, false)
			}
			users := []string{}
			for k := range knownusers {
				users = append(users, k)
			}
			log.Printf("current users: %v", users)
		case chat.PartMessage:
			farewells := map[string]string{}
			for k, v := range msg.Users() {
				farewells[k] = v
				delete(knownusers, k)
			}
			chat.TrimBots(farewells)
			for k := range farewells {
				log.Printf("user %s has left", k)
				tw.Farewell(channelName, k)
			}
			users := []string{}
			for k := range knownusers {
				users = append(users, k)
			}
			log.Printf("current users: %v", users)
		}
	}
}
