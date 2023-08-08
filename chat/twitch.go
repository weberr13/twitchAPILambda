package chat

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/weberr13/twitchAPILambda/config"
)

var (
	// ErrAuthFailed is returned if the twitch authetication process returns an error
	ErrAuthFailed = fmt.Errorf("failed to authenticate")
	// ErrNoChannel must provide a channel to join
	ErrNoChannel = fmt.Errorf("no channel provided")
	// ErrNoConnection connection was not established
	ErrNoConnection = fmt.Errorf("no connection")

	// TODO: Random weighted to one or the other?
	warewellMessages = []string{
		"farewell %s, we will miss you! weberrSenaWave",
		"%s left the stove on and has left the chat weberrSenaWave",
		"bye %s, later gater weberrSenaWave",
		"thanks for stopping by %s weberrSenaWave",
		"adios %s weberrSenaWave",
		"hasta la vista %s weberrSenaWave",
	}
	// TODO:
	// Add 18+ or even 21+ depending on the content classification data we get back
	// ContentClassificationLabels: "DrugsIntoxication", "ProfanityVulgarity", "ViolentGraphic"
	shoutoutMessages = []string{
		`Welcome %s and check them out at https://twitch.tv/%s they were last playing "%s" and show them some love weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Hello %s everyone should check out their channel at https://twitch.tv/%s they were last playing "%s" weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Show this legend %s some love, and drop them a follow at https://twitch.tv/%s they were last playing "%s" weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Check this amazing streamer %s out at https://twitch.tv/%s they were last playing "%s" weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Please show this wonderfull streamer %s some love at https://twitch.tv/%s they were last playing "%s" weberrSenaWow weberrSenaWow weberrSenaWow`,
	}
	// TwitchCharacterLimit is the maximum message size
	TwitchCharacterLimit = 500
)

// AlternateUsers for a given twitch user (eg a watching account separate from the broadcasting one)
func AlternateUsers() map[string]string {
	return map[string]string{
		"theradserver": "ripperzanddabs",
	}
}

// Bots we know about
func Bots() []string {
	return []string{
		"nightbot", "kattah", "streamfahrer", "einfachuwe42", "aliceydra", "drapsnatt",
		"commanderroot", "zkeey", "lurxx", "fwost", "implium", "vlmercy",
		"pokemoncommunitygame", "0ax2", "arctlco" /*maybe*/, "anotherttvviewer",
		"01ella", "own3d", "elbierro", "8hvdes", "7bvllet", "01olivia", "spofoh", "ahahahahhhhahahahahah",
	}
}

// TrimBots from a user list
func TrimBots(users map[string]string) {
	for _, bot := range Bots() {
		delete(users, bot)
	}
}

// Shoutout a user
func (t *Twitch) Shoutout(channelName string, user string, manual bool) {
	user = strings.TrimPrefix(user, "@")
	user = strings.ToLower(user)
	alt := user
	if a, ok := AlternateUsers()[user]; ok {
		alt = a
	}

	userInfo, err := t.GetUserInfo(alt)
	if err != nil {
		log.Printf("could not get user info, not doing a shoutout: %s", err)
		return
	}
	// log.Printf("%#v", userInfo)
	chanInfo, err := t.GetChannelInfo(userInfo)
	if err != nil {
		log.Printf("could not get channel info, not doing a shoutout: %s", err)
		return
	}
	if chanInfo.GameName == "" || chanInfo.GameName == "<none>" {
		log.Printf("not shouting out user as they don't stream")
		return
	}
	followed, err := t.IFollowThem(userInfo.ID)
	if err != nil {
		log.Printf("something whent wrong getting follow info: %s", err)
		return
	}
	if !followed && !manual {
		log.Printf("no auto shoutout for: %#v", chanInfo)
		return
	}
	if !followed && manual {
		log.Printf("I don't follow %#v %#v but shouting out regardless", userInfo, chanInfo)
	}
	iBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(shoutoutMessages))))
	messgeIndex := 0
	if err == nil {
		messgeIndex = int(iBig.Int64())
	}
	// TODO: Text Parsing {{.etc}}
	str := fmt.Sprintf(shoutoutMessages[messgeIndex], user, alt, chanInfo.GameName)

	err = t.SendMessage(channelName, str)
	if err != nil {
		log.Printf("could not auto-shoutout %s", user)
	}
}

// Farewell to a user
func (t *Twitch) Farewell(channelName string, user string) {
	user = strings.TrimPrefix(user, "@")
	iBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(warewellMessages))))
	messgeIndex := 0
	if err == nil {
		messgeIndex = int(iBig.Int64())
	}
	err = t.SendMessage(channelName, fmt.Sprintf(warewellMessages[messgeIndex], user))
	if err != nil {
		log.Printf("could not say goodbye to %s", user)
	}
}

// Twitch talks to twitch
type Twitch struct {
	c            *websocket.Conn
	cfg          *config.Configuration
	token        string
	hostUserInfo *TwitchUserInfo
}

// NewTwitch chat interface
func NewTwitch(cfg *config.Configuration) (*Twitch, error) {
	u := url.URL{Scheme: "wss", Host: cfg.GetChatWSS(), Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	return &Twitch{c: c, cfg: cfg}, nil
}

// TwitchUserInfo response from GetUser
// https://dev.twitch.tv/docs/api/reference/#get-users
type TwitchUserInfo struct {
	ID              string    `json:"id"`
	Login           string    `json:"login"`
	DisplayName     string    `json:"display_name"`
	Type            string    `json:"type"`
	BroadcasterType string    `json:"broadcaster_type"`
	Description     string    `json:"description"`
	ProfileImageURL string    `json:"profile_image_url"`
	OffilneImageURL string    `json:"offline_image_url"`
	ViewCount       int       `json:"view_count"`
	Email           string    `json:"email"`
	CreatedAt       time.Time `json:"created_at"`
}

// TwitchChannelInfo contains the information about a channel
type TwitchChannelInfo struct {
	BroadcasterID              string   `json:"broadcaster_id"`
	BroadcasterLogin           string   `json:"broadcaster_login"`
	BroadcasterName            string   `json:"broadcaster_name"`
	BroadcasterLanguage        string   `json:"broadcaster_language"`
	GameID                     string   `json:"game_id"`
	GameName                   string   `json:"game_name"`
	Title                      string   `json:"title"`
	Delay                      int      `json:"delay"`
	Taghs                      []string `json:"tags"`
	ContentClassificatinLables []string `json:"content_classification_labels"`
	ISBrandedContent           bool     `json:"is_branded_content"`
}

// {
// 	"broadcaster_id": "141981764",
// 	"broadcaster_login": "twitchdev",
// 	"broadcaster_name": "TwitchDev",
// 	"broadcaster_language": "en",
// 	"game_id": "509670",
// 	"game_name": "Science & Technology",
// 	"title": "TwitchDev Monthly Update // May 6, 2021",
// 	"delay": 0,
// 	"tags": ["DevsInTheKnow"],
// 	"content_classification_labels": ["Gambling", "DrugsIntoxication", "MatureGame"],
// 	"is_branded_content": false
//   }

// GetUserInfo gets the information on a user by login
func (t *Twitch) GetUserInfo(login string) (*TwitchUserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.twitch.tv/helix/users?login="+login, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot make request: %w", err)
	}
	t.cfg.SetAuthorization(req, t.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot do request: %w", err)
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode > http.StatusMultipleChoices {
		return nil, fmt.Errorf("got back %d on get users command", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	type respData struct {
		Data []*TwitchUserInfo `json:"data"`
	}
	userInfo := &respData{}
	err = json.Unmarshal(b, userInfo)
	if err != nil {
		return nil, err
	}
	if len(userInfo.Data) > 0 {
		return userInfo.Data[0], nil
	}
	return nil, fmt.Errorf("user %s not found", login)
}

// GetChannelInfo gets channel information
func (t *Twitch) GetChannelInfo(userInfo *TwitchUserInfo) (*TwitchChannelInfo, error) {
	if userInfo == nil || userInfo.ID == "" {
		return nil, fmt.Errorf("no user specified")
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.twitch.tv/helix/channels?broadcaster_id="+userInfo.ID, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot make request: %w", err)
	}
	t.cfg.SetAuthorization(req, t.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot do request: %w", err)
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode > http.StatusMultipleChoices {
		return nil, fmt.Errorf("got back %d on get users command", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	type respData struct {
		Data []*TwitchChannelInfo `json:"data"`
	}
	chanInfo := &respData{}
	err = json.Unmarshal(b, chanInfo)
	if err != nil {
		return nil, err
	}
	if len(chanInfo.Data) > 0 {
		return chanInfo.Data[0], nil
	}
	return nil, fmt.Errorf("user %#v not found", userInfo)
}

// IFollowThem checks if a channel is followed by the channel running the bot, used by auto-shoutout
// https://dev.twitch.tv/docs/api/reference/#get-followed-channels
func (t *Twitch) IFollowThem(theirID string) (bool, error) {
	if t.hostUserInfo == nil {
		hostUserInfo, err := t.GetUserInfo(t.cfg.Twitch.ChannelName)
		if err != nil {
			return false, err
		}
		t.hostUserInfo = hostUserInfo
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.twitch.tv/helix/channels/followed?user_id="+t.hostUserInfo.ID+"&broadcaster_id="+theirID, nil)
	if err != nil {
		return false, fmt.Errorf("cannot make request: %w", err)
	}
	t.cfg.SetAuthorization(req, t.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("cannot do request: %w", err)
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode > http.StatusMultipleChoices {
		return false, fmt.Errorf("got back %d on get users command", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return false, err
	}
	type respData struct {
		Total int                      `json:"total"`
		Data  []map[string]interface{} `json:"data"` // TODO: do we want this to be the real thing?
	}
	followInfo := &respData{}
	err = json.Unmarshal(b, followInfo)
	if err != nil {
		return false, err
	}
	return len(followInfo.Data) > 0, nil
}

// Close will idempotently close the underlying websocket
func (t *Twitch) Close() error {
	var err error
	if t.c != nil {
		err = t.c.Close()
		t.c = nil
	}
	return err
}

// SetChatOps configures the chat session for twitch streams
func (t *Twitch) SetChatOps() error {
	if t.c == nil {
		return ErrNoConnection
	}
	err := t.c.WriteMessage(websocket.TextMessage, []byte("CAP REQ :twitch.tv/membership twitch.tv/tags twitch.tv/commands"))
	if err != nil {
		return fmt.Errorf("could not pass the cap request %w", err)
	}
	msgType, b, err := t.c.ReadMessage()
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	if msgType == websocket.TextMessage {
		var msg TwitchMessage
		err := msg.Parse(b)
		if err != nil {
			return fmt.Errorf("failed to get chat ops response")
		}
	} else {
		fmt.Println("got unexpected message type in reply")
	}
	return nil
}

// Authenticate to twitch for the bot name with the given auth token
func (t *Twitch) Authenticate(name, token string) error {
	if t.c == nil {
		return ErrNoConnection
	}
	if token == "" {
		return fmt.Errorf("no valid token provided")
	}
	if name == "" {
		name = "weberr13"
	}
	passCmd := fmt.Sprintf("PASS oauth:%s", token)
	err := t.c.WriteMessage(websocket.TextMessage, []byte(passCmd))
	if err != nil {
		return fmt.Errorf("could not pass the authentication %w", err)
	}
	err = t.c.WriteMessage(websocket.TextMessage, []byte("NICK "+name))
	if err != nil {
		return fmt.Errorf("could not pass the nick %w", err)
	}
	msgType, b, err := t.c.ReadMessage()
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	if msgType == websocket.TextMessage {
		var msg TwitchMessage
		err := msg.Parse(b)
		if err != nil {
			return fmt.Errorf("failed to get auth response")
		}
		if msg.Type() == AuthenticationFail {
			return ErrAuthFailed
		}
	} else {
		return ErrAuthFailed
	}
	t.token = token
	return nil
}

// JoinChannels on an authenticated session
func (t *Twitch) JoinChannels(channels ...string) error {
	if t.c == nil {
		return ErrNoConnection
	}
	channelNames := ""
	if len(channels) == 0 {
		return ErrNoChannel
	}
	channelNames = "#" + channels[0]
	for i := 1; i < len(channels); i++ {
		channelNames += ",#"
		channelNames += channels[i]
	}
	log.Printf("attempting to join %s", channelNames)
	err := t.c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("JOIN %s", channelNames)))
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	msgType, b, err := t.c.ReadMessage()
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	if msgType == websocket.TextMessage {
		// todo parse for success
		var msg TwitchMessage
		err := msg.Parse(b)
		if err != nil {
			return fmt.Errorf("failed to get join response")
		}
	} else {
		return fmt.Errorf("got unexpected message type in reply")
	}
	return nil
}

// SendMessage to a channel TODO: elipsis/fmt args pls
func (t *Twitch) SendMessage(channelName, msg string) error {
	if t.c == nil {
		return ErrNoConnection
	}
	err := t.c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("PRIVMSG #%s :%s", channelName, msg)))
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	msgType, b, err := t.c.ReadMessage()
	if err != nil {
		return fmt.Errorf("could not get response %w", err)
	}
	if msgType == websocket.TextMessage {
		var msg TwitchMessage
		err := msg.Parse(b)
		if err != nil {
			return fmt.Errorf("failed to get send response")
		}
	} else {
		fmt.Println("got unexpected message type in reply")
	}
	return nil
}

// ReceiveOneMessage waits for a message to be posted to chat
func (t *Twitch) ReceiveOneMessage() (TwitchMessage, error) {
	msg := TwitchMessage{}
	if t.c == nil {
		return msg, ErrNoConnection
	}
	msgType, b, err := t.c.ReadMessage()
	if err != nil {
		return msg, fmt.Errorf("could not get message %w", err)
	}
	if msgType == websocket.TextMessage {
		err = msg.Parse(b)
		return msg, err
	}
	return msg, fmt.Errorf("got unexpected message type in reply")
}

// Pong is keep alive
func (t *Twitch) Pong(msg TwitchMessage) error {
	if t.c == nil {
		return ErrNoConnection
	}
	if msg.Type() != PingMessage {
		return nil
	}
	err := t.c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("PONG :%s", msg.Body())))
	if err != nil {
		return fmt.Errorf("could not send keep alive %w", err)
	}
	return nil
}
