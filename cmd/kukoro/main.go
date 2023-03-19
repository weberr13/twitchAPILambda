package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
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
	u := url.URL{Scheme: "wss", Host: ourConfig.GetChatWSS(), Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	for {
		req, err := http.NewRequest(http.MethodGet, ourConfig.OurURL+"?cmd=token", nil)
		if err != nil {
			log.Fatalf("cannot make request: %s", err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("cannot do request: %s", err)
		}
		tr := func() *config.TokenResponse {
			defer res.Body.Close()
			b, err := io.ReadAll(res.Body)
			if err != nil {
				return nil
			}
			tr := config.TokenResponse{}
			err = json.Unmarshal(b, &tr)
			if err != nil {
				return nil
			}
			return &tr
		}()
		if tr == nil || tr.Token == "" {
			fmt.Printf(`Please authorize or re-authorize the app by vistiting %s?name=%s&channel=%s&type=chat`, ourConfig.OurURL, channelName, channelID)
			fmt.Println("")
			time.Sleep(30 * time.Second)
			continue
		}
		break
	}

	// use token to auth to chat
	fmt.Println("hello world")
}
