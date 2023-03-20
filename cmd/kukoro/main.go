package main

import (
	"flag"
	"log"

	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/config"
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
	defer tw.Close()

	tr, err := ourConfig.GetAuthTokenResponse(channelID, channelName)
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return
	}
	err = tw.SetChatOps()
	if err != nil {
		log.Printf("could not set chat ops: %s", err)
		return
	}
	auth:
	for {
		log.Printf("attempting to authenticate")
		err = tw.Authenticate("weberr13", tr.Token)
		if err == chat.ErrAuthFailed {
			log.Printf("forcing token reauth")
			err = ourConfig.InvalidateToken(channelID, channelName)
			if err != nil {
				log.Printf("could not invalidate old token: %s", err)
				return
			}
			log.Printf("re-fetching auth token")
			tr, err = ourConfig.GetAuthTokenResponse(channelID, channelName)
			if err != nil {
				log.Printf("could not invalidate old token: %s", err)
				return
			}
			continue
		}
		log.Printf("authentication successful!")
		break auth
	}
	err = tw.JoinChannels(channelName)
	if err != nil {
		log.Printf("could not join channel on twitch: %s", err)
		return
	}
	err = tw.SendMessage(channelName, "!pokecheck")
	if err != nil {
		log.Printf("could not send message: %s", err)
		return
	}
}
