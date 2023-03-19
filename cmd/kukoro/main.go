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

	var tr *config.TokenResponse
	for {
		req, err := http.NewRequest(http.MethodGet, ourConfig.OurURL+"?cmd=chattoken", nil)
		if err != nil {
			log.Fatalf("cannot make request: %s", err)
		}
		req.Header.Set("Nightbot-Channel", fmt.Sprintf("providerId=%s", channelID))
		req.Header.Set("Nightbot-User", fmt.Sprintf("name=%s&displayName=%s&provider=twitch&providerId=%s&userLevel=moderator", channelName, channelName, channelID))
		req.Header.Set("ClientID", ourConfig.ClientID)
		req.Header.Set("ClientSecret", ourConfig.ClientSecret)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Fatalf("cannot do request: %s", err)
		}
		tr = func() *config.TokenResponse {
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

	err = c.WriteMessage(websocket.TextMessage, []byte("CAP REQ :twitch.tv/membership twitch.tv/tags twitch.tv/commands"))
	if err != nil {
		log.Fatalf("could not pass the cap request %s", err)
	}
	msgType, b, err := c.ReadMessage()
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	if msgType == websocket.TextMessage {
		fmt.Printf("got :%s", string(b))
		fmt.Println("")
	} else {
		fmt.Println("got unexpected message type in reply")
	}
	passCmd := fmt.Sprintf("PASS oauth:%s", tr.Token)
	err = c.WriteMessage(websocket.TextMessage, []byte(passCmd))
	if err != nil {
		log.Fatalf("could not pass the authentication %s", err)
	}
	err = c.WriteMessage(websocket.TextMessage, []byte("NICK Weberr13apibot"))
	if err != nil {
		log.Fatalf("could not pass the nick %s", err)
	}
	msgType, b, err = c.ReadMessage()
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	if msgType == websocket.TextMessage {
		// todo parse for success
		fmt.Printf("got :%s", string(b))
		fmt.Println("")
	} else {
		fmt.Println("got unexpected message type in reply")
	}
	err = c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("JOIN #%s", channelName)))
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	msgType, b, err = c.ReadMessage()
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	if msgType == websocket.TextMessage {
		// todo parse for success
		fmt.Printf("got :%s", string(b))
		fmt.Println("")
	} else {
		fmt.Println("got unexpected message type in reply")
	}
	err = c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("PRIVMSG #%s :Hello World!", channelName)))
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	msgType, b, err = c.ReadMessage()
	if err != nil {
		log.Fatalf("could not get response %s", err)
	}
	if msgType == websocket.TextMessage {
		// todo parse for success
		fmt.Printf("got :%s", string(b))
		fmt.Println("")
	} else {
		fmt.Println("got unexpected message type in reply")
	}
}
