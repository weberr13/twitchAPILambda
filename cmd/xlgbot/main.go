package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"math/big"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/weberr13/twitchAPILambda/autochat"
	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/discord"
	"github.com/weberr13/twitchAPILambda/kukoro"
	"github.com/weberr13/twitchAPILambda/obs"
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

// func authenticateLoop(tw *chat.Twitch, channelID, channelName string) (err error) {
// 	var tr *config.TokenResponse
// 	tr, err = ourConfig.GetAuthTokenResponse(channelID, channelName)
// 	if err == config.ErrNeedAuthorization {
// 		return err
// 	}
// 	if err != nil {
// 		log.Printf("could not get auth token %s", err)
// 		return err
// 	}
// 	err = tw.SetChatOps()
// 	if err != nil {
// 		log.Printf("could not set chat ops: %s", err)
// 		return err
// 	}
// 	auth:
// 	for {
// 		log.Printf("attempting to authenticate")
// 		err = tw.Authenticate("weberr13", tr.Token)
// 		if err == chat.ErrAuthFailed {
// 			log.Printf("forcing token reauth")
// 			err = ourConfig.InvalidateToken(channelID, channelName)
// 			if err != nil {
// 				log.Printf("could not invalidate old token: %s", err)
// 				return err
// 			}
// 			log.Printf("re-fetching auth token")
// 			tw, err = chat.NewTwitch(ourConfig)
// 			if err != nil {
// 				log.Fatalf("could not reach twitch: %s", err)
// 			}

// 			tr, err = ourConfig.GetAuthTokenResponse(channelID, channelName)
// 			if err != nil {
// 				log.Printf("could not get auth token %s", err)
// 				return err
// 			}
// 			err = tw.SetChatOps()
// 			if err != nil {
// 				log.Printf("could not set chat ops: %s", err)
// 				return err
// 			}
// 			continue
// 		}
// 		break auth
// 	}
// 	err = tw.JoinChannels(channelName)
// 	if err != nil {
// 		log.Printf("could not join channel on twitch: %s", err)
// 		return
// 	}
// 	err = tw.SendMessage(channelName, "xlg bot has joined")
// 	if err != nil {
// 		log.Printf("could not join channel on twitch: %s", err)
// 		return
// 	}
// 	return err
// }

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

	obsC, err := obs.NewClient(ourConfig.OBS.Password)
	if err != nil {
		log.Printf("could not start OBS websocket client: %s", err)
	} else {
		defer obsC.Close()
		v, err := obsC.GetVersion()
		if err != nil {
			log.Printf("could not get obs version: %s", err)
		} else {
			log.Printf("OBS Version: %s", v)
		}
		s, err := obsC.GetScenes()
		if err != nil {
			log.Printf("could not get obs scenes: %s", err)
		} else {
			log.Printf("OBS Scenes:\n%s", s)
		}
		scene, i, err := obsC.GetSourcesForCurrentScene()
		if err != nil {
			log.Printf("could not get obs sources: %s", err)
		} else {
			log.Printf("OBS Sources for %s:\n", scene)
			for _, source := range i {
				log.Printf("%s: %v", source.SourceName, *source)
			}
		}
	}
	tw, err := chat.NewTwitch(ourConfig)
	if err != nil {
		log.Fatalf("could not reach twitch: %s", err)
	}
	defer tw.Close()
	err = tw.AuthenticateLoop(channelID, channelName)
	if err == config.ErrNeedAuthorization {
		return
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return
	}
	appContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}
	recon := func() error {
		log.Printf("attempting to reconnect to twitch")
		log.Printf("attempting to close socket")
		err := tw.Close()
		if err != nil {
			log.Printf("closing twitch failed: %s", err)
			return err
		}
		log.Printf("attempting to reopen socket")
		err = tw.Open()
		if err != nil {
			log.Printf("repening twitch failed: %s", err)
			return err
		}
		log.Printf("attempting to authenticate")
		err = tw.AuthenticateLoop(channelID, channelName)
		if err == config.ErrNeedAuthorization {
			return err
		}
		if err != nil {
			log.Printf("could not get auth token %s", err)
			return err
		}
		return nil
	}
	discordBot.RunAutoShoutouts(appContext, wg, ourConfig.Discord.GoLiveChannels, func(users []string) (map[string]discord.StreamInfo, error) {
		m := make(map[string]discord.StreamInfo)
		twitchChans, err := tw.GetAllStreamInfoForUsers(users)
		if err != nil {
			log.Printf("could not get live channels for twitch: %s attempting to reconnect", err)
			// if the bot is only doing discord this will require a recon, if it is also in a channel
			// trying to recon here will cause a train wreak.  When adding discord only mode fix this
			// **** FIX ME ***
			// err = recon()
			// if err != nil {
			// 	return m, err
			// }
		}
		for user, st := range twitchChans {
			m[user] = discord.StreamInfo{
				UserLogin:    st.UserLogin,
				UserName:     st.UserName,
				GameName:     st.GameName,
				Type:         st.Type,
				Title:        st.Title,
				ViewerCount:  st.ViewerCount,
				StartedAt:    st.StartedAt,
				Language:     st.Language,
				ThumbnailURL: st.ThumbnailURL,
				IsMature:     st.IsMature,
			}
		}
		// Can support other platforms
		return m, nil
	})
	knownusers := map[string]string{}

	commands := map[string]func(msg chat.TwitchMessage){
		"reconnect": func(msg chat.TwitchMessage) {
			if msg.IsOwner() {
				err := recon()
				if err != nil {
					log.Printf("%s", err)
				}
				log.Printf("we should be reconnected")
			}
		},
		"promo": func(msg chat.TwitchMessage) {
			if msg.IsMod() {
				s := msg.GetBotCommandArgs()
				if s != "" {
					err := obsC.SetPromoYoutube(ourConfig.LocalOBS.PromoSource, s)
					if err != nil {
						log.Printf("could set promo: %s", err)
					}
				}
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err := obsC.TogglePromo(ourConfig.LocalOBS.PromoSource)
				if err != nil {
					log.Printf("could not run promo: %s", err)
				}
			}
		},
		"clip": func(msg chat.TwitchMessage) {
			if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
				_ = tw.SendMessage(channelName, "a clip is being processed, give twitch time...")
				r, err := tw.Clip()
				if err != nil {
					log.Printf("could not clip: %s", err)
				}
				go func() {
					time.Sleep(15 * time.Second)
					_ = tw.SendMessage(channelName, r)
				}()
			}
		},
		"pcghelp": func(msg chat.TwitchMessage) {
			_ = tw.SendMessage(channelName, "*** !pokestart - start playing, stay in chat for more poke$ *** !pokepass - check your balance *** !pokeshop pokeball|greatball|ultraball # - buy # of the specified ball *** !pokecatch greatball|ultraball - use a better ball than pokeball or premiere ball *** !pokecheck - see if you have caught the pokemon before (look for Check or X) *** stay active in chat for poke$")
		},
		"youtube": func(msg chat.TwitchMessage) {
			if ourConfig.Twitch.YouTube != "" {
				_ = tw.SendMessage(channelName, "Subscribe to my Youtube for more content and edited streams "+ourConfig.Twitch.YouTube)
			} else {
				log.Printf("no youtube configured: %v", ourConfig.Twitch)
			}
		},
		"socials": func(msg chat.TwitchMessage) {
			if len(ourConfig.Twitch.Socials) > 0 {
				s := "When I'm not streaming find me at "
				for i, url := range ourConfig.Twitch.Socials {
					if i > 0 {
						s += " | "
					}
					s += url
				}
				_ = tw.SendMessage(channelName, s)
			} else {
				log.Printf("no socials configured: %v", ourConfig.Twitch)
			}
		},
		"github": func(msg chat.TwitchMessage) {
			_ = tw.SendMessage(channelName, "To checkout the source for this go to https://github.com/weberr13/twitchAPILambda")
		},
		"ask": func(msg chat.TwitchMessage) {
			if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
				func() {
					_ = tw.SendMessage(channelName, "The oracle has heard your question, please wait...")
					ctx, cancel := context.WithTimeout(appContext, 30*time.Second)
					defer cancel()
					user := strings.TrimPrefix(msg.User(), ":")
					log.Printf(`requestor "%s" msg:"%#v"`, user, msg)
					resp, err := autoChatter.CreateCompletion(ctx, msg.GetBotCommandArgs(), autochat.WithRateLimit(user, 5*time.Minute))
					if err != nil {
						log.Printf("openai failed: %s", err)
						_ = tw.SendMessage(channelName, "I cannot answer that right now, Dave")
						return
					}
					// TODO: this spilt failed, I missed the middle when it split 3 times.  Write a test
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
							time.Sleep(1 * time.Second) // maybe we sent messages too fast?
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
		},
		"bye": func(msg chat.TwitchMessage) {
			if msg.IsMod() {
				tw.Farewell(channelName, msg.GetBotCommandArgs())
			} else {
				log.Printf("got bye command from %s", msg.GoString())
			}
		},
		"so": func(msg chat.TwitchMessage) {
			if msg.IsMod() {
				tw.Shoutout(channelName, msg.GetBotCommandArgs(), true)
			} else {
				log.Printf("got so command from %s", msg.GoString())
			}
		},
		// todo alias? "rm":
		"raidmsg": func(msg chat.TwitchMessage) {
			if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
				// TODO: put this in config?
				err = tw.SendMessage(channelName, "Weberr13 RAID weberrMioRaid weberrMioRaid weberrMioRaid")
				if err != nil {
					log.Printf("could not send raid message %s: %s", msg.DisplayName(), err)
				}
			}
		},
		// TODO Alias: "srm":
		"subraid": func(msg chat.TwitchMessage) {
			if msg.IsMod() || msg.IsSub() || msg.IsVIP() {
				err = tw.SendMessage(channelName, "Weberr13 RAID weberrMioRaid weberrMioCheer weberrMioRaid")
				if err != nil {
					log.Printf("could not send raid message %s: %s", msg.DisplayName(), err)
				}
			}
		},
		"whois": func(msg chat.TwitchMessage) {
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
		},
		"kukoro": func(msg chat.TwitchMessage) {
			err = tw.SendMessage(channelName, "!getinfo "+msg.DisplayName())
			if err != nil {
				log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
			}
		},
		"jump": func(msg chat.TwitchMessage) {
			err = tw.SendMessage(channelName, "!getinfo "+msg.DisplayName())
			if err != nil {
				log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
			}
		},
		"vote": func(msg chat.TwitchMessage) {
			err = tw.SendMessage(channelName, "!getinfo "+msg.GetBotCommandArgs())
			if err != nil {
				log.Printf("could not send getinfo for %s: %s", msg.DisplayName(), err)
			}
		},
		"h": func(msg chat.TwitchMessage) {
			err = tw.SendMessage(channelName, fmt.Sprintf("%s has voited for another dungeon raid", msg.DisplayName()))
			if err != nil {
				log.Printf("could not send vote info for %s: %s", msg.DisplayName(), err)
			}
		},
	}
	for newCmd, detail := range ourConfig.Twitch.Commands {
		newCmd := newCmd
		detail := detail
		if detail.Valid() {
			commands[strings.TrimPrefix(newCmd, "!")] = func(msg chat.TwitchMessage) {
				err = tw.SendMessage(channelName, detail.GetText())
				if err != nil {
					log.Printf("could run custom command %s: %s", newCmd, err)
				}
			}
			for _, aka := range detail.CommandAliases() {
				aka := aka
				commands[strings.TrimPrefix(aka, "!")] = func(msg chat.TwitchMessage) {
					err = tw.SendMessage(channelName, detail.GetText())
					if err != nil {
						log.Printf("could run custom command %s: %s", aka, err)
					}
				}
			}
		} else {
			log.Printf("found unexpected command %s: %v", newCmd, detail)
		}
	}
	commands["getcommands"] = func(msg chat.TwitchMessage) {
		// TODO: commands should have descriptions
		allCmds := []string{}
		for k := range commands {
			allCmds = append(allCmds, fmt.Sprintf("!%s", k))
		}
		err = tw.SendMessage(channelName, strings.Join(allCmds, ", "))
		if err != nil {
			log.Printf("could run getcommands: %s", err)
		}
	}

	for _, timer := range ourConfig.Twitch.Timers {
		// wg.Add(1)
		if timer.Alias == "" && timer.Message == "" {
			log.Printf("got empty timer %v", timer)
			continue
		}
		go func(timerConfig config.TimerConfig) {
			iBig, err := rand.Int(rand.Reader, big.NewInt(600)) // TODO: make this configurable
			jitterSec := 1
			if err == nil {
				jitterSec = int(iBig.Int64())
			}
			log.Printf("timer %#v waiting %d seconds before start", timerConfig, jitterSec)
			time.Sleep(time.Duration(jitterSec) * time.Second)
			tick := time.NewTimer(timerConfig.WaitFor())
			defer tick.Stop()
			for {
				select {
				case <-appContext.Done():
					return
				case <-tick.C:
					func() {
						defer tick.Reset(timerConfig.WaitFor())
						log.Printf("running timer %#v", timerConfig)
						if timerConfig.Alias != "" {
							body := timerConfig.Alias
							if len(timerConfig.Message) > 0 {
								body += " " + timerConfig.Message
							}
							msg := chat.FakeTwitchMessage(body)
							if f, ok := commands[timerConfig.Alias[1:]]; ok {
								f(msg)
								return
							}
						}
						_ = tw.SendMessage(channelName, timerConfig.Message)
					}()
				}
			}
		}(timer)
	}

	// TODO : we need shutdown logic now... sadly
readloop:
	for {
		msg, err := tw.ReceiveOneMessage()
		if err == chat.ErrInvalidMsg {
			log.Printf("could not parse message %s: %s", msg.Raw(), err)
			continue
		} else if err != nil {
			err = recon()
			if err != nil {
				log.Printf("%s", err)
				return
			}
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
					pokename := msg.Body()
					// ACTION OhMyDog A wild Snubbull appears OhMyDog Catch it using !pokecatch (winners revealed in 90s)
					i := strings.Index(pokename, "A wild ")
					j := strings.Index(pokename, " appears")
					if i > 0 && j > i {
						pokename = pokename[i+len("A wild ") : j]
					}
					urlName := regexp.MustCompile(`[^a-z0-9 ]+`).ReplaceAllString(strings.ToLower(pokename), "")
					urlName = strings.ReplaceAll(urlName, " ", "-")

					for _, ch := range ourConfig.Discord.PCGChannels {
						msg, err := discordBot.SendMessage(ch, fmt.Sprintf("A wild [%s](https://www.pokemon.com/us/pokedex/%s) has spawned in %s, go to https://twitch.tv/%s to catch it", pokename, urlName, channelName, channelName))
						if err != nil {
							log.Printf("could not post pokemon spawn: %s", err)
						} else {
							go func(chanID, msgID string) {
								time.Sleep(90 * time.Second)
								err := discordBot.DeleteMessage(chanID, msgID)
								if err != nil {
									log.Printf("could not delete spawn message: %s", err)
								}
							}(msg.ChannelID, msg.ID)
						}
					}

				}
			case msg.User() == "weberr13":
				if kukoro.IsKukoroMsg(msg) {
					fmt.Println("Kukoro says: ", msg.Body())
					continue readloop
				}
			case msg.IsBotCommand():
				if f, ok := commands[msg.GetBotCommand()]; ok {
					f(msg)
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
			// newchatters := map[string]string{}
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
