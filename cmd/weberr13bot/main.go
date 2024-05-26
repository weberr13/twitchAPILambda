package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/weberr13/twitchAPILambda/autochat"
	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/db"
	"github.com/weberr13/twitchAPILambda/discord"
	"github.com/weberr13/twitchAPILambda/kukoro"
	"github.com/weberr13/twitchAPILambda/obs"
	"github.com/weberr13/twitchAPILambda/pcg"
)

var (
	ourConfig   *config.Configuration
	channelName string
	channelID   string
	// StatusMessagePrefix is the prefix you use for storing status messages, the other half is a channelid
	StatusMessagePrefix = "status-"
)

func init() {
	ourConfig = config.NewConfig()
	ourConfig.Clean()
}

// RunTimer runs a timer
func RunTimer(ctx context.Context, wg *sync.WaitGroup, t *config.TimerConfig, commands map[string]func(msg chat.TwitchMessage), sendF func(message string), toggleC chan struct{}) {
	defer wg.Done()
	iBig, err := rand.Int(rand.Reader, big.NewInt(600)) // TODO: make this configurable, make a command to turn them off and on for owner to run
	jitterSec := 1
	if err == nil {
		jitterSec = int(iBig.Int64())
	}
	// log.Printf("timer %#v waiting %d seconds before start", t, jitterSec)
startloop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-toggleC:
			log.Printf("currently enabled == %v, togging", t.Enabled())
			t.ToggleEnabled()
		case <-time.After(time.Duration(jitterSec) * time.Second):
			break startloop
		}
	}
	tick := time.NewTimer(t.WaitFor())
	defer tick.Stop()
	runt := func() {
		if !t.Enabled() {
			tick.Reset(t.WaitFor())
			return
		}
		func() {
			defer tick.Reset(t.WaitFor())
			log.Printf("running timer %#v", t)
			if t.Alias != "" {
				body := t.Alias
				if len(t.Message) > 0 {
					body += " " + t.Message
				}
				msg := chat.FakeTwitchMessage(body)
				if f, ok := commands[t.Alias[1:]]; ok {
					f(msg)
					return
				}
			}
			sendF(t.Message)
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-toggleC:
			log.Printf("currently enabled == %v, togging", t.Enabled())
			t.ToggleEnabled()
			runt()
		case <-tick.C:
			runt()
		}
	}
}

func contextClose(ctx context.Context, wg *sync.WaitGroup, closer io.Closer) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		// log.Printf("closing %#v", closer)
		err := closer.Close()
		if err != nil {
			log.Printf("problem closing %#v %s", closer, err)
		}
	}()
}

// ShoutOutUser keeps track of the RealName of the new users who qualify for shoutouts and when they were last shouted
type ShoutOutUser struct {
	RealName     string
	LastShoutout time.Time
}

func runAllClips(done chan struct{}, user string, tw *chat.Twitch, obsC *obs.Client) {
	userInfo, err := tw.GetUserInfo(user)
	if err != nil {
		log.Printf("could not get user info, not doing a shoutout: %s", err)
		return
	}
	clips, err := tw.GetClips(userInfo)
	if err != nil {
		log.Printf("could not get clips: %s", err)
		return
	}
	for _, clip := range clips {
		select {
		case <-done:
			return
		default:
			log.Printf("ready to run clip %v", clip)
		}
		// Pick one from above
		err = obsC.SetPromoTwitch(ourConfig.LocalOBS.PromoSource, clip.EmbeddURL+"&autoplay=true&parent=obs.com")
		if err != nil {
			log.Printf("could not set promo: %s", err)
			return
		}
		err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
		if err != nil {
			log.Printf("could not toggle audio: %s", err)
		}
		err = obsC.TogglePromo(ourConfig.LocalOBS.PromoSource)
		if err != nil {
			log.Printf("could not run promo: %s", err)
		}
		time.Sleep(time.Duration(100*clip.Duration) * time.Second / 100) // duration of clip
		err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
		if err != nil {
			log.Printf("could not toggle audio: %s", err)
		}
		err := obsC.TogglePromo(ourConfig.LocalOBS.PromoSource)
		if err != nil {
			log.Printf("could not run promo: %s", err)
		}
	}
}

func mainloop(ctx context.Context, wg *sync.WaitGroup, tw *chat.Twitch,
	discordBot *discord.BotClient, obsC *obs.Client, autoChatter *autochat.OpenAI, persist db.Persister,
) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		wtw := pcg.NewWonderTradeWatcher(persist, tw)
		wtw.Run(channelName)
		defer wtw.Close()
		knownusers := sync.Map{}
		shoutouts := sync.Map{}
		clipModeEnabled := false
		var lastScene string
		clipC := make(chan struct{}, 2)
		r := mrand.New(mrand.NewSource(time.Now().Unix()))
		betCooldown := map[string]time.Time{}

		commands := map[string]func(msg chat.TwitchMessage){
			"toggle": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					tm := strings.Fields(msg.GetBotCommandArgs())
					if len(tm) > 0 {
						timername := tm[0]
						log.Printf("toggle %s sending", timername)
						if _, ok := ourConfig.Twitch.Timers[timername]; ok {
							if ourConfig.Twitch.Timers[timername].ToggleC != nil {
								ourConfig.Twitch.Timers[timername].ToggleC <- struct{}{}
							} else {
								log.Printf("could not send toggle, no toggle channel")
							}
						}
					}
				}
			},
			"reconnect": func(msg chat.TwitchMessage) {
				if msg.IsOwner() {
					err := tw.Reconnect(ctx, channelID, channelName)
					if err != nil {
						log.Printf("%s", err)
					}
					log.Printf("we should be reconnected")
				}
			},
			"checkLive": func(msg chat.TwitchMessage) {
				if msg.IsOwner() {
					if config.IsLive.Load() {
						_ = tw.SendMessage(channelName, "We are live!")
					} else {
						_ = tw.SendMessage(channelName, "We are dead!")
					}
				}
			},
			"juteboxVolume": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					s := msg.GetBotCommandArgs()
					val, err := strconv.ParseFloat(s, 64)
					if err != nil {
						log.Printf("could not set audio, invalid value: %s", err)
						return
					}
					err = obsC.SetSourceVolume(ourConfig.LocalOBS.MusicSource, val)
					if err != nil {
						log.Printf("could not set audio: %s", err)
					}
				}
			},
			"clips": func(msg chat.TwitchMessage) {
				if msg.IsOwner() {
					if clipModeEnabled {
						log.Printf("stopping clips")
						clipModeEnabled = false
						clipC <- struct{}{}
						return
					}
					log.Printf("starting clips")
					clipModeEnabled = true
					go runAllClips(clipC, channelName, tw, obsC)
				}
			},
			"promo": func(msg chat.TwitchMessage) {
				if clipModeEnabled {
					return
				}
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
			"discord": func(msg chat.TwitchMessage) {
				if ourConfig.Twitch.Discord != "" {
					if config.IsLive.Load() {
						_ = tw.SendMessage(channelName, "Join me on discord at "+ourConfig.Twitch.Discord)
					} else {
						_ = tw.SendMessage(channelName, "Find me on discord at "+ourConfig.Twitch.Discord)
					}
				} else {
					log.Printf("no discord configured: %#v", ourConfig.Twitch)
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
						ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
			"gamble": func(msg chat.TwitchMessage) {
				user := msg.AtUser()
				user = strings.TrimPrefix(user, "@")
				user = strings.ToLower(user)
				wt := db.Points{
					User: user,
				}
				var err error
				if msg.IsOwner() {
					wt.Points = 6942042371337
				} else {
					c, ok := betCooldown[user]
					if ok && time.Since(c) < 60*time.Second {
						return
					}
					err = persist.Get(wt.Key(), &wt)
				}
				betCooldown[user] = time.Now()
				if err == nil {
					if wt.Points == 0 {
						_ = tw.SendMessage(channelName, fmt.Sprintf("%s has no points to gamble, stay active in chat for more points", wt.User))
					}
					args := msg.GetBotCommandArgs()
					if args == "" {
						_ = tw.SendMessage(channelName, "usage !gamble [points,%%,all]")
						return
					}
					var err error
					wager := uint64(0)
					switch {
					case args == "all":
						wager = wt.Points
					case strings.HasSuffix(args, "%"):
						p := strings.TrimSuffix(args, "%")
						i, err := strconv.ParseFloat(p, 64)
						if err != nil {
							_ = tw.SendMessage(channelName, "usage !gamble [points,%%,all]")
							return
						}
						if i < 0 {
							_ = tw.SendMessage(channelName, "usage !gamble [points,%%,all]")
							return
						}
						if i > 100 {
							i = 100.0
						}
						wager = uint64(float64(wt.Points) * i / 100.0)
					default:
						wager, err = strconv.ParseUint(args, 10, 64)
						if err != nil {
							_ = tw.SendMessage(channelName, "usage !gamble [points,%%,all]")
							return
						}
						if wager > wt.Points {
							wager = wt.Points
						}
					}
					draw := r.Int() % 100
					log.Printf("draw was %d", draw)
					if draw >= 49 {
						if draw >= 95 {
							wt.Points = wt.Points + 2*wager
							_ = tw.SendMessage(channelName, fmt.Sprintf("%s rolled %d Critical Success! and won %d and now has %d points", wt.User, draw, 2*wager, wt.Points))

						} else {
							wt.Points = wt.Points + wager
							_ = tw.SendMessage(channelName, fmt.Sprintf("%s rolled %d and won %d and now has %d points", wt.User, draw, wager, wt.Points))
						}
					} else {
						wt.Points = wt.Points - wager
						if draw < 5 {
							_ = tw.SendMessage(channelName, fmt.Sprintf("%s rolled %d Critial Failure!! no gambling for 10 minutes! and lost %d and now has %d points", wt.User, draw, wager, wt.Points))
							betCooldown[user] = betCooldown[user].Add(9 * 60 * time.Second)
						} else {
							_ = tw.SendMessage(channelName, fmt.Sprintf("%s rolled %d and lost %d and now has %d points", wt.User, draw, wager, wt.Points))
						}
					}
					err = persist.Put(wt.Key(), wt)
					if err != nil {
						log.Printf("error putting points %v: %s", wt, err)
					}

				} else {
					log.Printf("got %s checking points %v", err, wt)
					_ = tw.SendMessage(channelName, fmt.Sprintf("%s has points unknown", wt.User))
					return
				}
			},
			"hide": func(msg chat.TwitchMessage) {
				if msg.IsMod() || msg.IsOwner() {
					err := obsC.ToggleSourceVisible("Display Capture Blur")
					if err != nil {
						// blur not found, probably the wrong scene
						return
					}
					_ = obsC.ToggleSourceVisible("Display Capture Desktop")
				}
			},
			"points": func(msg chat.TwitchMessage) {
				user := msg.CleanUserFromArgs()
				wt := db.Points{
					User: user,
				}
				err := persist.Get(wt.Key(), &wt)
				if err == nil {
					_ = tw.SendMessage(channelName, fmt.Sprintf("%s has %d points", wt.User, wt.Points))
				} else {
					log.Printf("got %s checking points %v", err, wt)
					_ = tw.SendMessage(channelName, fmt.Sprintf("%s has points unknown", wt.User))
				}
			},
			"watchtime": func(msg chat.TwitchMessage) {
				user := msg.CleanUserFromArgs()
				wt := db.Watchtime{
					User: user,
				}
				err := persist.Get(wt.Key(), &wt)
				if err == nil {
					_ = tw.SendMessage(channelName, fmt.Sprintf("%s has a watchtime of %s", wt.User, wt.Time.String()))
				} else {
					log.Printf("got %s checking watchtime %v", err, wt)
					_ = tw.SendMessage(channelName, fmt.Sprintf("%s has a watchtime of unknown", wt.User))
				}
			},
			"brb": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					err = obsC.ToggleInputVolume(ourConfig.LocalOBS.MicroSource)
					if err != nil {
						log.Printf("could not toggle microphone: %s", err)
					}
					currentScene, err := obsC.GetActiveScene()
					if err != nil {
						log.Printf("could not get active scene: %s", err)
					}
					if currentScene == ourConfig.LocalOBS.BRBScene {
						if lastScene == "" {
							log.Printf("can't switch back to nothing!!!")
						} else {
							err := obsC.SetActiveScene(lastScene)
							if err == nil {
								lastScene = ""
								err = obsC.ResumeRecording()
								if err != nil {
									_ = tw.SendMessage(channelName, "warning: trouble resuming recording")
									log.Printf("could not resume recording: %s", err)
								}
							} else {
								log.Printf("failed to switch scene: %s", err)
							}
						}
					} else {
						err := obsC.SetActiveScene(ourConfig.LocalOBS.BRBScene)
						if err == nil {
							lastScene = currentScene
							err = obsC.PauseRecording()
							if err != nil {
								_ = tw.SendMessage(channelName, "warning: trouble pausing recording")
								log.Printf("could not pause recording: %s", err)
							}
						} else {
							log.Printf("failed to switch scene: %s", err)
						}
					}
					// switch to BRB scene
				}
			},
			"sso": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					user := msg.GetBotCommandArgs()
					userSplit := strings.Fields(user)
					if len(userSplit) > 0 {
						user = userSplit[0]
					}
					if so, ok := shoutouts.Load(user); ok {
						sso, _ := so.(ShoutOutUser)
						sso.LastShoutout = time.Now()
						shoutouts.Store(user, sso)
					} else {
						shoutouts.Store(user, ShoutOutUser{LastShoutout: time.Now()})
					}
					clips := tw.SuperShoutOut(channelName, user, true)
					if len(clips) > 0 {
						iBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(clips))))
						var clip *chat.TwithcClipInfo
						if err != nil {
							log.Printf("could not generate random number %s", err)
							clip = clips[0]
						} else {
							log.Printf("playing clip %d", iBig.Int64())
							clip = clips[iBig.Int64()]
						}
						// Pick one from above
						err = obsC.SetPromoTwitch(ourConfig.LocalOBS.PromoSource, clip.EmbeddURL+"&autoplay=true&parent=obs.com")
						if err != nil {
							log.Printf("could not set promo: %s", err)
							return
						}
						err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
						if err != nil {
							log.Printf("could not toggle audio: %s", err)
						}
						err = obsC.TogglePromo(ourConfig.LocalOBS.PromoSource)
						if err != nil {
							log.Printf("could not run promo: %s", err)
						}
						go func(duration float64) {
							time.Sleep(time.Duration(100*duration) * time.Second / 100) // duration of clip
							err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
							if err != nil {
								log.Printf("could not toggle audio: %s", err)
							}
							err := obsC.TogglePromo(ourConfig.LocalOBS.PromoSource)
							if err != nil {
								log.Printf("could not run promo: %s", err)
							}
						}(clip.Duration)
					}
				} else {
					log.Printf("got so command from %s", msg.GoString())
				}
			},
			"lurk": func(msg chat.TwitchMessage) {
				_ = tw.SendMessage(channelName, fmt.Sprintf("%s has retreated into their shell", msg.AtUser()))
			},
			"unlurk": func(msg chat.TwitchMessage) {
				_ = tw.SendMessage(channelName, fmt.Sprintf("%s is ready to party", msg.AtUser()))
			},
			"ban": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
					if err != nil {
						log.Printf("could not toggle audio: %s", err)
					}
					err = obsC.TogglePromo("YouLose") // TODO: put this in the config?
					if err != nil {
						log.Printf("could not run ban source: %s", err)
					}
					time.Sleep(10 * time.Second) // duration of clip put this in config?
					err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
					if err != nil {
						log.Printf("could not toggle audio: %s", err)
					}
					err := obsC.TogglePromo("YouLose")
					if err != nil {
						log.Printf("could not run ban source: %s", err)
					}
				}
			},
			"songs": func(msg chat.TwitchMessage) {
				if msg.IsMod() && strings.Contains(msg.Body(), "skip") {
					err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
					if err != nil {
						log.Printf("could not toggle audio: %s", err)
					}
					err = obsC.TogglePromo("ChangeIT") // TODO: put this in the config?
					if err != nil {
						log.Printf("could not run skip source: %s", err)
					}
					time.Sleep(6 * time.Second) // duration of clip put this in config?
					err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
					if err != nil {
						log.Printf("could not toggle audio: %s", err)
					}
					err := obsC.TogglePromo("ChangeIT")
					if err != nil {
						log.Printf("could not run skip source: %s", err)
					}
				}
			},
			"wt": func(msg chat.TwitchMessage) {
				after := 3 * time.Hour
				override := msg.GetBotCommandArgs()
				if len(override) > 0 {
					if override == "delete" {
						wt := pcg.NewWonderTradeReminder(time.Now(), msg.AtUser())
						_ = persist.Delete(wt.Key())
						_ = tw.SendMessage(channelName,
							fmt.Sprintf("your reminder is removed %s, you are on your own now", wt.Username))
						return
					}
					d, err := time.ParseDuration(override)
					if err == nil && d < after {
						after = d
					}
				}

				wt := pcg.NewWonderTradeReminder(time.Now(), msg.AtUser(), after)
				err := persist.Get(wt.Key(), wt)
				if err == nil {
					_ = tw.SendMessage(channelName,
						fmt.Sprintf("your reminder is already set %s, I will remind you around %s", wt.Username, wt.Deadline.Format(time.RFC3339)))
					return
				}
				err = persist.Put(wt.Key(), wt)
				if err == nil {
					_ = tw.SendMessage(channelName,
						fmt.Sprintf("setting wonder trade reminder for %s I will remind you around %s", wt.Username, wt.Deadline.Format(time.RFC3339)))
				} else {
					_ = tw.SendMessage(channelName, "I'm sorry I can't do that right now, Dave")
				}
			},
			"so": func(msg chat.TwitchMessage) {
				if msg.IsMod() {
					user := msg.GetBotCommandArgs()
					userSplit := strings.Fields(user)
					if len(userSplit) > 0 {
						user = userSplit[0]
					}
					if so, ok := shoutouts.Load(user); ok {
						sso, _ := so.(ShoutOutUser)
						sso.LastShoutout = time.Now()
						shoutouts.Store(user, sso)
					} else {
						shoutouts.Store(user, ShoutOutUser{LastShoutout: time.Now()})
					}
					tw.Shoutout(channelName, user, true)
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
					knownusers.Range(func(k any, _ any) bool {
						users = append(users, k.(string))
						return true
					})
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

		for name, timer := range ourConfig.Twitch.Timers {
			// wg.Add(1)
			if timer.Alias == "" && timer.Message == "" {
				log.Printf("got empty timer %v", timer)
				continue
			}
			ourConfig.Twitch.Timers[name].ToggleC = make(chan struct{}, 5)
			wg.Add(1)
			go RunTimer(ctx, wg, timer, commands, func(s string) { _ = tw.SendMessage(channelName, s) }, ourConfig.Twitch.Timers[name].ToggleC)
		}

		getPointRedemption := func(pointValue uint64) func(ctx context.Context, m chat.TwitchPointRedemption) {
			return func(ctx context.Context, m chat.TwitchPointRedemption) {
				points := db.Points{
					User: m.Redemption.User.DisplayName,
				}
				err := persist.Get(points.Key(), &points)
				if err == nil || err == db.ErrNotFound {
					points.Points += pointValue
				} else {
					log.Printf("Get points redemption failed %#v: %v", points, err)
					return
				}
				err = persist.Put(points.Key(), points)
				if err != nil {
					log.Printf("Get points redemption failed %#v: %v", points, err)
					return
				}
				knownusers.Range(func(k any, _ any) bool {
					user, ok := k.(string)
					if !ok {
						return false
					}
					points := db.Points{
						User: user,
					}
					err := persist.Get(points.Key(), &points)
					if err == nil || err == db.ErrNotFound {
						points.Points += pointValue
					} else {
						log.Printf("Get points redemption failed %#v: %v", points, err)
						return false
					}
					err = persist.Put(points.Key(), points)
					if err != nil {
						log.Printf("Get points redemption failed %#v: %v", points, err)
						return false
					}
					return true
				})
			}
		}

		redemptionHandlers := map[string]func(context.Context, chat.TwitchPointRedemption){
			"Get 1000 points":    getPointRedemption(1000),
			"Get 10000 points":   getPointRedemption(10000),
			"Get 100000 points":  getPointRedemption(100000),
			"Get 1000000 points": getPointRedemption(1000000),
			"Melly's Garden Checkin": func(ctx context.Context, _ chat.TwitchPointRedemption) {
				log.Printf("got Melly checkin")
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err = obsC.TogglePromo("MellyCheckin") // TODO: put this in the config?
				if err != nil {
					log.Printf("could not run melly checkin: %s", err)
				}
				time.Sleep(54 * time.Second) // duration of clip put this in config?
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err := obsC.TogglePromo("MellyCheckin")
				if err != nil {
					log.Printf("could not run melly checkin: %s", err)
				}
			},
			"Panda Pals Checkin": func(ctx context.Context, _ chat.TwitchPointRedemption) {
				log.Printf("got Panda checkin")
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err = obsC.TogglePromo("PandaPals") // TODO: put this in the config?
				if err != nil {
					log.Printf("could not run panda pals checkin: %s", err)
				}
				time.Sleep(24 * time.Second) // duration of clip put this in config?
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err := obsC.TogglePromo("PandaPals")
				if err != nil {
					log.Printf("could not run panda pals checkin: %s", err)
				}
			},
			"Team No Sleep": func(ctx context.Context, _ chat.TwitchPointRedemption) {
				log.Printf("got team no sleep checkin")
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err = obsC.TogglePromo("Yawn") // TODO: put this in the config?
				if err != nil {
					log.Printf("could not run nosleep checkin: %s", err)
				}
				time.Sleep(24 * time.Second) // duration of clip put this in config?
				err = obsC.ToggleSourceAudio(ourConfig.LocalOBS.MusicSource)
				if err != nil {
					log.Printf("could not toggle audio: %s", err)
				}
				err := obsC.TogglePromo("Yawn")
				if err != nil {
					log.Printf("could not run nosleep checkin: %s", err)
				}
			},
		}
		tw.StartPubSubEventHandler(ctx, wg, redemptionHandlers)
		log.Printf("starting chat handler")
		lastChecked := time.Now()
		pointCooldown := map[string]time.Time{}
	readloop:
		for {
			if ctx.Err() != nil {
				log.Printf("got shutdown mesage")
				return
			}
			log.Printf("reading a chat message")
			msg, err := tw.ReceiveOneMessage()
			if err == chat.ErrInvalidMsg {
				log.Printf("could not parse message %s: %s", msg.Raw(), err)
				continue
			} else if err != nil {
				err = tw.Reconnect(ctx, channelID, channelName)
				if err != nil {
					log.Printf("%s", err)
					return
				}
			}
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
						discordBot.SendPokemonMessage(msg.Body(), channelName, ourConfig.Discord.PCGChannels)
					}
				case msg.User() == "weberr13":
					if kukoro.IsKukoroMsg(msg) {
						fmt.Println("Kukoro says: ", msg.Body())
						continue readloop
					}
				case msg.IsBotCommand():
					if !config.IsLive.Load() && !msg.IsOwner() {
						continue
					}
					if f, ok := commands[msg.GetBotCommand()]; ok {
						f(msg)
					}
					log.Printf("command: %s, args: %s", msg.GetBotCommand(), msg.GetBotCommandArgs())
				default:
					if !config.IsLive.Load() && !msg.IsOwner() {
						log.Printf("ignoring non-owner messages while offline")
						continue
					}
					user := strings.TrimPrefix(msg.User(), ":")
					// log.Printf("checking for autoshoutout for %s in %#v", user, shoutouts)
					if so, ok := shoutouts.Load(user); ok {
						sso, _ := so.(ShoutOutUser)
						// log.Printf("last shoutout was %v", so.LastShoutout)
						if time.Since(sso.LastShoutout) > 120*time.Minute {
							log.Printf("running shoutout for %s", user)
							tw.Shoutout(channelName, user, false)
							sso.LastShoutout = time.Now()
							shoutouts.Store(user, sso)
						}
					}
					if !chat.IsBot(user) {
						c, ok := pointCooldown[user]
						if !ok || time.Since(c) > (5*60*time.Second) {
							points := db.Points{
								User: user,
							}
							_ = persist.Get(points.Key(), &points)
							if msg.IsSub() {
								points.Points += 50
							}
							points.Points += 50
							err = persist.Put(points.Key(), points)
							if err != nil {
								log.Printf("failed to safe points for %#v: %s", points, err)
							}
						}
						pointCooldown[user] = time.Now()
					}

					log.Printf(`%s says: "%s"`, msg.DisplayName(), msg.Body())
				}
			case chat.PingMessage:
				err := tw.Pong(msg)
				if err != nil {
					log.Printf("could not keep connection alive: %s", err)
					return
				}
				if config.IsLive.Load() {
					knownusers.Range(func(k any, _ any) bool {
						user, ok := k.(string)
						if !ok {
							return false
						}
						if chat.IsBot(user) {
							return true
						}
						wt := db.Watchtime{
							User: user,
						}
						err := persist.Get(wt.Key(), &wt)
						if err == db.ErrNotFound {
							wt.Time = time.Since(lastChecked)
						} else if err == nil {
							wt.Time += time.Since(lastChecked)
						}
						err = persist.Put(wt.Key(), wt)
						if err != nil {
							log.Printf("could not save %#v", wt)
						}
						log.Printf("updating watchtime for %s: %v", user, wt.Time)
						return true
					})
				}
				lastChecked = time.Now()
			case chat.JoinMessage:
				for k, v := range msg.Users() {
					if _, ok := knownusers.Load(k); !ok {
						if _, ok := shoutouts.Load(k); !ok {
							shoutouts.Store(k, ShoutOutUser{RealName: v})
						}
					}
					knownusers.Store(k, v)
				}
				chat.TrimBots(&knownusers)
				chat.TrimBots(&shoutouts)
				users := []string{}
				knownusers.Range(func(key any, _ any) bool {
					user, ok := key.(string)
					if !ok {
						return false
					}
					users = append(users, user)
					return true
				})
				log.Printf("current users: %v", users)
				// log.Printf("current shoutouts: %#v", shoutouts)
			case chat.PartMessage:
				farewells := sync.Map{} // time?
				for k, v := range msg.Users() {
					farewells.Store(k, v)
					knownusers.Delete(k)
				}
				chat.TrimBots(&farewells)
				farewells.Range(func(k any, _ any) bool {
					user, ok := k.(string)
					if !ok {
						return false
					}
					log.Printf("user %s has left", user)
					tw.Farewell(channelName, user)
					return true
				})
				users := []string{}
				knownusers.Range(func(k any, _ any) bool {
					user, ok := k.(string)
					if !ok {
						return false
					}
					users = append(users, user)
					return true
				})
				log.Printf("current users: %v", users)
			}
		}
	}()
}

func main() {
	appContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}

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
	var discordBot *discord.BotClient
	var err error
	persist, err := db.NewBadger("./")
	if err != nil {
		log.Printf("cannot persist things!!! %s", err)
		return
	}

	defer persist.Close()

	statusMessages := map[string]*discordgo.Message{}
	keys, err := persist.PrefixScan(StatusMessagePrefix)
	if err == nil {
		for _, key := range keys {
			msg := discordgo.Message{}
			err := persist.Get(key, &msg)
			if err == nil {
				statusMessages[strings.TrimPrefix(key, StatusMessagePrefix)] = &msg
			}
		}
	}

	discordBot, err = discord.NewBot(*ourConfig.Discord, autoChatter, persist)
	if err != nil {
		log.Printf("not starting discord bot: %s", err)
	} else {
		statusF := func() {
			str := fmt.Sprintf("weberr13 discord bot is running. last seen: <t:%d:F>", time.Now().Unix())
			for ch, msg := range statusMessages {
				statusMessages[ch], _ = discordBot.UpdateMessage(msg, str)
				_ = persist.Put(StatusMessagePrefix+ch, statusMessages[ch])
			}
			newSends := []string{}
			for _, ch := range ourConfig.Discord.LogChannels {
				_, ok := statusMessages[ch]
				if ok {
					continue // already have one
				}
				newSends = append(newSends, ch)
			}
			msgs, err := discordBot.BroadcastMessage(newSends, str)
			if err != nil {
				log.Printf("could not send discord test message: %s", err)
			}
			for _, msg := range msgs {
				statusMessages[msg.ChannelID] = msg
				_ = persist.Put(StatusMessagePrefix+msg.ChannelID, msg)
			}
			_ = persist.Sync()
		}
		statusF()
		go func() {
			tick := time.NewTicker(10 * time.Minute)
			defer tick.Stop()
			for {
				select {
				case <-appContext.Done():
					for ch, msg := range statusMessages {
						if msg != nil {
							_ = discordBot.DeleteMessage(ch, msg.ID)
							_ = persist.Delete(StatusMessagePrefix + ch)
						}
					}
					_ = persist.Sync()
					contextClose(appContext, wg, discordBot)
				case <-tick.C:
					statusF()
				}
			}
		}()
	}

	obsC, err := obs.NewClient(ourConfig.OBS.Password)
	if err != nil {
		log.Fatalf("could not start OBS websocket client: %s", err)
	} else {
		contextClose(appContext, wg, obsC)
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
		// scene, i, err := obsC.GetSourcesForCurrentScene()
		// if err != nil {
		// 	log.Printf("could not get obs sources: %s", err)
		// } else {
		// 	log.Printf("OBS Sources for %s:\n", scene)
		// 	for _, source := range i {
		// 		log.Printf("%s: %v", source.SourceName, *source)
		// 	}
		// }
	}
	tw, err := chat.NewTwitch(ourConfig)
	if err != nil {
		log.Fatalf("could not reach twitch: %s", err)
	}
	err = tw.GetAuthTokens(appContext, channelID, channelName)
	if err == config.ErrNeedAuthorization {
		return // we gave up
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return
	}
	err = tw.Open(appContext)
	if err != nil {
		log.Fatalf("could not open connection to twitch %s", err)
	}
	contextClose(appContext, wg, tw)

	if discordBot != nil && channelName == "weberr13" { // for now this only runs on my machine
		discordBot.RunAutoShoutouts(appContext, wg, ourConfig.Discord.GoLiveChannels,
			func(users []string) (map[string]discord.StreamInfo, error) {
				m := make(map[string]discord.StreamInfo)
				log.Printf("getting live status for %#v", users)
				twitchChans, code, err := tw.GetAllStreamInfoForUsers(users...)
				if err != nil {
					if code == http.StatusUnauthorized {
						log.Printf("could not get live channels for twitch: %s attempting to reconnect", err)
						err = tw.Reconnect(appContext, channelID, channelName)
						if err != nil {
							return m, err
						}
					} else {
						for i := 0; i < 10; i++ {
							log.Printf("could not get live channels for twitch: %s not attempting to reconnect", err)
							twitchChans, code, err = tw.GetAllStreamInfoForUsers(users...)
							if err == nil {
								break
							} else if code == http.StatusUnauthorized {
								log.Printf("could not get live channels for twitch: %s attempting to reconnect", err)
								err = tw.Reconnect(appContext, channelID, channelName)
								if err != nil {
									return m, err
								}
							}
						}
					}
				}
				if err != nil {
					// err = tw.Reconnect(appContext, channelID, channelName)
					// if err != nil {
					// 	return m, err
					// }
					return nil, err
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
			},
			func(t string) []string {
				return nil
				// https://twitch.uservoice.com/forums/310213-developers/suggestions/43079766-stream-title-searching
				// infos, code, err := tw.SearchChannels(t)
				// if err != nil {
				// 	log.Printf("failed search for %s with code %d: %s", t, code, err)
				// 	return nil
				// }
				// channels := []string{}
				// for _, info := range infos {
				// 	channels = append(channels, info.BroadcasterLogin)
				// }
				// return channels
			},
			persist)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("got signal %s", sig)
		go func() {
			time.Sleep(10 * time.Second)
			os.Exit(1)
		}()
		cancel()
	}()

	log.Printf("starting main chat loop")
	wg.Add(1)
	go func(tw *chat.Twitch) {
		defer wg.Done()
		liveT := time.NewTimer(5 * time.Second)
		defer liveT.Stop()
		for {
			select {
			case <-liveT.C:
				userInfo, code, err := tw.GetAllStreamInfoForUsers(channelName)
				if err != nil {
					log.Printf("could not get live status for ourselves %d: %s", code, err)
					liveT.Reset(60 * time.Second)
					continue
				}
				if u, ok := userInfo[channelName]; ok {
					switch {
					case config.IsLive.Load() && u.Type == "live":
						liveT.Reset(5 * 60 * time.Second)
					case config.IsLive.Load() && u.Type != "live":
						config.IsLive.Store(false)
						liveT.Reset(30 * time.Second)
					case !config.IsLive.Load() && u.Type == "live":
						config.IsLive.Store(true)
						liveT.Reset(10 * 60 * time.Second)
					case !config.IsLive.Load() && u.Type != "live":
						liveT.Reset(30 * time.Second)
					}
				} else {
					config.IsLive.Store(false)
					liveT.Reset(30 * time.Second)
				}
			case <-appContext.Done():
				return
			}
		}
	}(tw)
	mainloop(appContext, wg, tw, discordBot, obsC, autoChatter, persist)
	wg.Wait()
}

// 2024/01/07 17:01:54 failure to send channel message &discordgo.MessageSend{Content:"",
// Embeds:[]*discordgo.MessageEmbed{(*discordgo.MessageEmbed)(0xc00044a140)},
//  TTS:false, Components:[]discordgo.MessageComponent{(*discordgo.ActionsRow)(0xc000008558)},
//  Files:[]*discordgo.File(nil), AllowedMentions:(*discordgo.MessageAllowedMentions)(nil), Reference:(*discordgo.MessageReference)(nil),
//  File:(*discordgo.File)(nil),
//   Embed:(*discordgo.MessageEmbed)(nil)}, HTTP 400 Bad Request,
//   {"message": "Invalid Form Body", "code": 50035, "errors": {"components": {"0":
//   {"components": {"0": {"emoji": {"name": {"_errors": [{"code": "BUTTON_COMPONENT_INVALID_EMOJI", "message": "Invalid emoji"}]}}}}}}}}
