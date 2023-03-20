package config

import (
	_ "embed" // embed the config in the binary for now
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
)

//go:embed config.json
var configBytes []byte

var (
	clipScope = []string{"clips:edit"}
	chatScope = []string{"chat:edit", "chat:read"}
)

// LevelAsNumber maps user levels to a number with owner == 0
var LevelAsNumber = map[string]int{
	"owner":      0,
	"moderator":  1,
	"vip":        2,
	"regular":    3,
	"subscriber": 4,
	"everyone":   5,
}

// Configuration embedded at build time
type Configuration struct {
	ClientSecret       string            `json:"clientSecret"`
	ClientID           string            `json:"clientID"`
	OurURL             string            `json:"ourURL"`
	TableName          string            `json:"tableName"`
	RedirectURL        string            `json:"redirect"`
	SignSecret         string            `json:"signSecret"`
	AuthorizedChannels map[string]string `json:"authorizedChannels"`
}

// TokenResponse contains the server side cached token
type TokenResponse struct {
	Token string `json:"token"`
}

// ToNum converts a level string to int
func ToNum(level string) int {
	n, ok := LevelAsNumber[strings.ToLower(level)]
	if !ok {
		return 6
	}
	return n
}

// GetClipOauth gets an oauth configuration suitable for clipping on twitch
func (c Configuration) GetClipOauth() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       clipScope,
		Endpoint:     twitch.Endpoint,
		RedirectURL:  c.RedirectURL,
	}
}

// GetChatOauth gets an oauth configuration sutable for chat bots
func (c Configuration) GetChatOauth() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       chatScope,
		Endpoint:     twitch.Endpoint,
		RedirectURL:  c.RedirectURL,
	}
}

// SetAuthorization sets the authorization headers for https requests to twitch api
func (c Configuration) SetAuthorization(req *http.Request, token string) {
	req.Header.Add("Authorization", "Bearer "+token)
	req.Header.Add("Client-Id", c.ClientID)
}

// GetChatWSS gets the websocket url for the twitch chat server
func (c Configuration) GetChatWSS() string {
	return "irc-ws.chat.twitch.tv:443"
}

// NewConfig from the embedded json
func NewConfig() *Configuration {
	ourConfig := &Configuration{}
	err := json.Unmarshal(configBytes, ourConfig)
	if err != nil {
		log.Fatal(err)
	}
	if ourConfig.RedirectURL == "" {
		ourConfig.RedirectURL = ourConfig.OurURL + "redirect"
	}
	if ourConfig.SignSecret == "" {
		ourConfig.SignSecret = "simpleSecret"
	}
	for id, level := range ourConfig.AuthorizedChannels {
		if ToNum(level) > 4 {
			ourConfig.AuthorizedChannels[id] = "owner"
			log.Printf("overriding user level to owner for %s, must be at least a regular to use the commands", id)
		}
	}
	return ourConfig
}

func (c Configuration) setCommandHeaders(req *http.Request, channelID, channelName string) {
	req.Header.Set("Nightbot-Channel", fmt.Sprintf("providerId=%s", channelID))
	req.Header.Set("Nightbot-User", fmt.Sprintf("name=%s&displayName=%s&provider=twitch&providerId=%s&userLevel=moderator", channelName, channelName, channelID))
	req.Header.Set("ClientID", c.ClientID)
	req.Header.Set("ClientSecret", c.ClientSecret)
}

// InvalidateToken that failed to authenticate previously
func (c Configuration) InvalidateToken(channelID, channelName string) error {
	req, err := http.NewRequest(http.MethodGet, c.OurURL+"?cmd=delchattoken", nil)
	if err != nil {
		return fmt.Errorf("cannot make request: %w", err)
	}
	c.setCommandHeaders(req, channelID, channelName)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot do request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("could not invalidate old token, fatal error")
	}
	return nil
}

// GetAuthTokenResponse from the backend server
func (c Configuration) GetAuthTokenResponse(channelID, channelName string) (*TokenResponse, error) {
	var tr *TokenResponse
	for {
		req, err := http.NewRequest(http.MethodGet, c.OurURL+"?cmd=chattoken", nil)
		if err != nil {
			return nil, fmt.Errorf("cannot make request: %w", err)
		}
		c.setCommandHeaders(req, channelID, channelName)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("cannot do request: %w", err)
		}
		tr = func() *TokenResponse {
			defer res.Body.Close()
			b, err := io.ReadAll(res.Body)
			if err != nil {
				return nil
			}
			tr := TokenResponse{}
			err = json.Unmarshal(b, &tr)
			if err != nil {
				return nil
			}
			return &tr
		}()
		if tr == nil || tr.Token == "" {
			fmt.Printf(`Please authorize or re-authorize the app by vistiting %s?name=%s&channel=%s&type=chat`, c.OurURL, channelName, channelID)
			fmt.Println("")
			time.Sleep(30 * time.Second)
			continue
		}
		break
	}
	return tr, nil
}
