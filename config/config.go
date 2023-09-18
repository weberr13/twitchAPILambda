package config

import (
	"context"
	_ "embed" // embed the config in the binary for now
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
)

//go:embed config.json
var configBytes []byte

var (
	clipScope = []string{"clips:edit"}
	chatScope = []string{
		"clips:edit",
		"chat:edit",
		"chat:read",
		"user:read:follows",
		"channel:read:redemptions",
		"bits:read",
		"channel:read:subscriptions",
	}
	// ErrNeedAuthorization is returned because we need to restart after auth is created
	ErrNeedAuthorization = fmt.Errorf("user needs to authorize the app")
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

// OBS websocket config
type OBS struct {
	// HOST:Port not yet configurable, use localhost 4455
	Password string `json:"password"`
}

// DiscordBotConfig configures a registered discord bot
type DiscordBotConfig struct {
	ApplicationID     string              `json:"applicationID"`
	PublicKey         string              `json:"publicKey"`
	Token             string              `json:"token"`
	BroadcastChannels []string            `json:"broadcastChannels"`
	PCGChannels       []string            `json:"pcgChannels"`
	ReplyChannels     []string            `json:"replyChannels"`
	LogChannels       []string            `json:"logChannels"`
	GoLiveChannels    map[string][]string `json:"goLiveChannels"` // channel -> list of users
}

// TwitchConfig params that allow a twitch bot to run
type TwitchConfig struct {
	ChannelName string                   `json:"channelName"`
	ChannelID   string                   `json:"channelID"`
	YouTube     string                   `json:"youtube"`
	Socials     []string                 `json:"socials"`
	Timers      map[string]*TimerConfig  `json:"timers"`
	Commands    map[string]CommandConfig `json:"commands"`
}

// CommandConfig is a simple command configuration
type CommandConfig struct {
	Message string   `json:"message"`
	AKA     []string `json:"aka"`
}

// Valid determines if a custom command is valid
func (cc *CommandConfig) Valid() bool {
	return cc.Message != ""
}

// GetText gives the text that the command should send to the channel
func (cc *CommandConfig) GetText() string {
	if cc.Message != "" {
		return cc.Message
	}
	return "I don't know how to do that, Dave"
}

// CommandAliases also know as commands
func (cc *CommandConfig) CommandAliases() []string {
	return cc.AKA
}

// TimerConfig configures a timer
type TimerConfig struct {
	WaitTime string        `json:"waittime"`
	Message  string        `json:"message"`
	Alias    string        `json:"alias"`
	ToggleC  chan struct{} `json:"-"`
	disabled bool          `json:"-"`
	sync.RWMutex
}

// WaitFor returns a parsed wait time or the minumum time of 5seconds
func (t *TimerConfig) WaitFor() time.Duration {
	d, err := time.ParseDuration(t.WaitTime)
	if err != nil || d < 5*time.Second {
		return 5 * time.Second
	}
	return d
}

// Enabled is the timer currently enabled
func (t *TimerConfig) Enabled() bool {
	t.RLock()
	defer t.RUnlock()
	return !t.disabled
}

// ToggleEnabled will disable/enable the timer
func (t *TimerConfig) ToggleEnabled() {
	t.Lock()
	defer t.Unlock()
	t.disabled = !t.disabled
}

// LocalOBS settings ie: scenes and sources
type LocalOBS struct {
	MusicSource string `json:"musicSource"`
	PromoSource string `json:"promoSource"`
	MicroSource string `json:"micSource"`
	BRBScene    string `json:"brbScene"`
}

// Configuration embedded at build time
type Configuration struct {
	ClientSecret       string            `json:"clientSecret"`
	ClientID           string            `json:"clientID"`
	OurURL             string            `json:"ourURL"`
	OpenAIKey          string            `json:"openAIKey"`
	TableName          string            `json:"tableName"`
	RedirectURL        string            `json:"redirect"`
	SignSecret         string            `json:"signSecret"`
	AuthorizedChannels map[string]string `json:"authorizedChannels"`
	Discord            *DiscordBotConfig `json:"discord,omitempty"`
	Twitch             TwitchConfig      `json:"twitch"`
	OBS                OBS               `json:"obs"`
	LocalOBS           LocalOBS          `json:"localOBS"`
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

// GetPubSubWSS gets the websocket (for pubsub events
func (c Configuration) GetPubSubWSS() string {
	return "pubsub-edge.twitch.tv:443"
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
	if ourConfig.Twitch.Timers == nil {
		ourConfig.Twitch.Timers = map[string]*TimerConfig{
			"xlg": {
				WaitTime: "30m",
				Message:  "Join the XLG gaming community at https://discord.gg/xlg",
			},
		}
	}
	// TODO: command line arg to find config file?
	localCfg, err := os.Open("local.cfg")
	if err == nil {
		b, err := io.ReadAll(localCfg)
		if err == nil {
			localConfig := &Configuration{}
			err = json.Unmarshal(b, localConfig)
			if err == nil {
				if localConfig.Twitch.ChannelID != "" {
					ourConfig.Twitch.ChannelID = localConfig.Twitch.ChannelID
				}
				if localConfig.Twitch.ChannelName != "" {
					ourConfig.Twitch.ChannelName = localConfig.Twitch.ChannelName
				}
				if localConfig.Twitch.YouTube != "" {
					ourConfig.Twitch.YouTube = localConfig.Twitch.YouTube
				}
				if len(localConfig.Twitch.Socials) > 0 {
					ourConfig.Twitch.Socials = localConfig.Twitch.Socials
				}
				if len(localConfig.Twitch.Timers) > 0 {
					for k, v := range localConfig.Twitch.Timers {
						ourConfig.Twitch.Timers[k] = v
					}
				}
				if len(localConfig.Twitch.Commands) > 0 {
					ourConfig.Twitch.Commands = make(map[string]CommandConfig)
					for k, v := range localConfig.Twitch.Commands {
						ourConfig.Twitch.Commands[k] = v
					}
				}
				ourConfig.LocalOBS = localConfig.LocalOBS // No globals
			} else {
				panic(err)
			}
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
func (c Configuration) InvalidateToken(ctx context.Context, channelID, channelName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.OurURL+"?cmd=delchattoken", nil)
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
func (c Configuration) GetAuthTokenResponse(ctx context.Context, channelID, channelName string) (*TokenResponse, error) {
	var tr *TokenResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.OurURL+"?cmd=chattoken", nil)
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
		// fmt.Printf("got: %s", string(b))
		tr := TokenResponse{}
		err = json.Unmarshal(b, &tr)
		if err != nil {
			return nil
		}
		return &tr
	}()
	if tr == nil || tr.Token == "" {
		authURL := fmt.Sprintf(`%s?name=%s&channel=%s&type=chat`, c.OurURL, channelName, channelID)
		_ = open(authURL)
		fmt.Printf("Check your default browser and allow the bot to access your chat and restart.  If your browser does not open visit %s by hand.", authURL)
		fmt.Println("")

		return nil, ErrNeedAuthorization
	}

	return tr, nil
}

// open opens the specified URL in the default browser of the user.
func open(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
		// others? https://www.robvanderwoude.com/escapechars.php
		args = append(args, strings.ReplaceAll(url, "&", "^&"))
	case "darwin":
		cmd = "open"
		args = append(args, url)
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
		args = append(args, url)
	}
	cmdE := exec.Command(cmd, args...)
	err := cmdE.Start()
	if err != nil {
		return err
	}
	return cmdE.Wait()
}
