package chat

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/weberr13/twitchAPILambda/config"
	twitchapi "github.com/weberr13/twitchAPILambda/twitch"
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
		"%s may be trying to lurk but failed to keep the tab open weberrNoahCry",
	}
	// TODO:
	// Add 18+ or even 21+ depending on the content classification data we get back
	// ContentClassificationLabels: "DrugsIntoxication", "ProfanityVulgarity", "ViolentGraphic", "SexualThemes", "MatureGame"
	shoutoutMessages = []string{
		`Welcome %s and check them out at https://twitch.tv/%s they were last playing %s and show them some love weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Hello %s everyone should check out their channel at https://twitch.tv/%s they were last playing %s weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Show this legend %s some love, and drop them a follow at https://twitch.tv/%s they were last playing %s weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Check this amazing streamer %s out at https://twitch.tv/%s they were last playing %s weberrSenaWow weberrSenaWow weberrSenaWow`,
		`Please show this wonderfull streamer %s some love at https://twitch.tv/%s they were last playing %s weberrSenaWow weberrSenaWow weberrSenaWow`,
	}
	eighteenPlusWarnings = map[string]string{
		"DrugsIntoxication":  `drug and alcohol use`,
		"ProfanityVulgarity": `profanity`,
		"ViolentGraphic":     `graphic violence`,
		"SexualThemes":       `sexual themes`,
		"MatureGame":         `mature games`,
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
		"commanderroot", "zkeey", "lurxx", "fwost", "implium", "vlmercy", "rogueg1rl",
		"pokemoncommunitygame", "0ax2", "arctlco" /*maybe*/, "anotherttvviewer", "morgane2k7", "01aaliyah",
		"01ella", "own3d", "elbierro", "8hvdes", "7bvllet", "01olivia", "spofoh", "ahahahahhhhahahahahah",
	}
}

// TrimBots from a user list
func TrimBots(users map[string]string) {
	for _, bot := range Bots() {
		delete(users, bot)
	}
}

func randIndex[T any](array []T) int {
	iBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(array))))
	messgeIndex := 0
	if err == nil {
		messgeIndex = int(iBig.Int64())
	}
	return messgeIndex
}

// Clip the latest few seconds
// nightbot : $(urlfetch https://m7tthg2fz8.execute-api.us-east-1.amazonaws.com/?cmd=clip)
func (t *Twitch) Clip() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://api.twitch.tv/helix/clips?broadcaster_id=%s", t.cfg.Twitch.ChannelID), nil)
	if err != nil {
		return "", err
	}
	t.authorizeRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("not authorized?")
	}
	m := twitchapi.CreateClipResponse{}
	b, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(b, &m)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= http.StatusMultiStatus {
		m2 := twitchapi.APIError{}
		err = json.Unmarshal(b, &m2)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("something went wrong: %s", m2.Message)
	}
	r := ""
	for _, d := range m.Data {
		if d.EditURL != "" {
			r += fmt.Sprintf("successfully created clip with id:%s and url:%s. Use the URL to adjust timing and duration.", d.ID, d.EditURL)
		}
	}
	if r != "" {
		return r, nil
	}

	return "", fmt.Errorf("unexpected response from TwitchAPI: %s", string(b))
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
	// log.Printf("*** channel info %#v", chanInfo)
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
	log.Printf("%s has content warnings: %#v", userInfo.Login, chanInfo.ContentClassificatinLables)
	gameTitle := `"` + chanInfo.GameName + `"`
	if len(chanInfo.ContentClassificatinLables) > 0 {
		gameTitle += "--but be warned that this channel has 18+ content that includes "
		for i, label := range chanInfo.ContentClassificatinLables {
			if i > 0 {
				gameTitle += ", "
			}
			gameTitle += eighteenPlusWarnings[label]
		}
		gameTitle += "--"
	}
	messgeIndex := randIndex(shoutoutMessages)

	// TODO: Text Parsing {{.etc}}
	str := fmt.Sprintf(shoutoutMessages[messgeIndex], user, alt, gameTitle)

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
	url          url.URL
	sync.RWMutex
}

// NewTwitch chat interface
func NewTwitch(cfg *config.Configuration) (*Twitch, error) {
	u := url.URL{Scheme: "wss", Host: cfg.GetChatWSS(), Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	return &Twitch{c: c, cfg: cfg, url: u}, nil
}

// Open the connection again
func (t *Twitch) Open() error {
	t.Lock()
	defer t.Unlock()
	if t.c != nil {
		return nil
	}
	c, _, err := websocket.DefaultDialer.Dial(t.url.String(), nil)
	if err != nil {
		return err
	}
	t.c = c
	return nil
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

// GetUserInfo gets the information on a user by login
func (t *Twitch) GetUserInfo(login string) (*TwitchUserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.twitch.tv/helix/users?login="+login, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot make request: %w", err)
	}
	t.authorizeRequest(req)
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
	t.authorizeRequest(req)
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

// TwitchStreamInfo contains info that twitch api provides on live streams
type TwitchStreamInfo struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	UserLogin    string    `json:"user_login"`
	UserName     string    `json:"user_name"`
	GameID       string    `json:"game_id"`
	GameName     string    `json:"game_name"`
	Type         string    `json:"type"` // "live"
	Title        string    `json:"title"`
	Tags         []string  `json:"tags"`
	ViewerCount  int       `json:"viewer_count"`
	StartedAt    time.Time `json:"started_at"`
	Language     string    `json:"language"`
	ThumbnailURL string    `json:"thumbnail_url"`
	TagIDs       []string  `json:"tag_ids"`
	IsMature     bool      `json:"is_mature"`
}

func (t *Twitch) authorizeRequest(req *http.Request) {
	t.RLock()
	defer t.RUnlock()
	t.cfg.SetAuthorization(req, t.token)
}

// GetAllStreamInfoForUsers will give the stream info for the given channel names
// curl -X GET 'https://api.twitch.tv/helix/streams'
// https://dev.twitch.tv/docs/api/reference/#get-streams
func (t *Twitch) GetAllStreamInfoForUsers(usernames []string) (map[string]TwitchStreamInfo, error) {
	m := make(map[string]TwitchStreamInfo)
	if len(usernames) < 1 {
		return m, nil
	}
	if len(usernames) > 100 {
		return nil, fmt.Errorf("twitch API limits requests to 100 users at a time")
	}
	reqString := "https://api.twitch.tv/helix/streams"
	for i, user := range usernames {
		if i == 0 {
			reqString += "?"
		} else {
			reqString += "&"
		}
		reqString += "user_login=" + user
	}
	reqString += fmt.Sprintf("&first=%d", len(usernames))
	req, err := http.NewRequest(http.MethodGet, reqString, nil)
	if err != nil {
		return m, fmt.Errorf("cannot make request: %w", err)
	}
	t.authorizeRequest(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return m, fmt.Errorf("cannot do request: %w", err)
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode > http.StatusMultipleChoices {
		return m, fmt.Errorf("got back %d on get streams command", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return m, err
	}
	type respData struct {
		Data []TwitchStreamInfo `json:"data"` // TODO: do we want this to be the real thing?
	}
	streams := &respData{}
	err = json.Unmarshal(b, streams)
	if err != nil {
		return m, err
	}
	for _, stream := range streams.Data {
		m[stream.UserLogin] = stream
	}
	return m, nil
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
	t.authorizeRequest(req)
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
	t.Lock()
	defer t.Unlock()
	var err error
	if t.c != nil {
		err = t.c.Close()
		t.c = nil
	}
	return err
}

// SetChatOps configures the chat session for twitch streams
func (t *Twitch) SetChatOps() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("SetChatOps failed %v", r)
		}
	}()
	t.Lock()
	defer t.Unlock()
	if t.c == nil {
		return ErrNoConnection
	}
	err = t.c.WriteMessage(websocket.TextMessage, []byte("CAP REQ :twitch.tv/membership twitch.tv/tags twitch.tv/commands"))
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

// AuthenticateLoop will try very hard to auth and join a channel
func (t *Twitch) AuthenticateLoop(channelID, channelName string) (err error) {
	var tr *config.TokenResponse
	tr, err = t.cfg.GetAuthTokenResponse(channelID, channelName)
	if err == config.ErrNeedAuthorization {
		return err
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return err
	}
	err = t.SetChatOps()
	if err != nil {
		log.Printf("could not set chat ops: %s", err)
		return err
	}
auth:
	for {
		log.Printf("attempting to authenticate")
		err = t.Authenticate("xlgbot", tr.Token) // twitch seems to ignore this and set it to whatever the user running the bot used to auth
		if err == ErrAuthFailed {
			log.Printf("forcing token reauth")
			err = t.cfg.InvalidateToken(channelID, channelName)
			if err != nil {
				log.Printf("could not invalidate old token: %s", err)
				return err
			}
			log.Printf("re-fetching auth token")
			err := t.Close()
			if err != nil {
				log.Fatalf("could not reach twitch: %s", err)
			}
			err = t.Open()
			if err != nil {
				log.Fatalf("could not reach twitch: %s", err)
			}

			tr, err = t.cfg.GetAuthTokenResponse(channelID, channelName)
			if err != nil {
				log.Printf("could not get auth token %s", err)
				return err
			}
			err = t.SetChatOps()
			if err != nil {
				log.Printf("could not set chat ops: %s", err)
				return err
			}
			continue
		}
		break auth
	}
	err = t.JoinChannels(channelName)
	if err != nil {
		log.Printf("could not join channel on twitch: %s", err)
		return
	}
	err = t.SendMessage(channelName, "xlg bot has joined")
	if err != nil {
		log.Printf("could not join channel on twitch: %s", err)
		return
	}
	return err
}

// Authenticate to twitch for the bot name with the given auth token
func (t *Twitch) Authenticate(name, token string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authenticate failed %v", r)
		}
	}()
	t.Lock()
	defer t.Unlock()
	if t.c == nil {
		return ErrNoConnection
	}
	if token == "" {
		return fmt.Errorf("no valid token provided")
	}
	if name == "" {
		name = "xlgbot" // twitch seems to ignore this and set it to whatever the user running the bot used to auth
	}
	passCmd := fmt.Sprintf("PASS oauth:%s", token)
	err = t.c.WriteMessage(websocket.TextMessage, []byte(passCmd))
	if err != nil {
		return fmt.Errorf("could not pass the authentication %w", err)
	}
	log.Printf("attempting to set nick to %s", name)
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
func (t *Twitch) JoinChannels(channels ...string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("join failed %v", r)
		}
	}()
	t.Lock()
	defer t.Unlock()
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
	err = t.c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("JOIN %s", channelNames)))
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
func (t *Twitch) SendMessage(channelName, msg string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("send failed %v", r)
		}
	}()
	t.Lock()
	defer t.Unlock()
	if t.c == nil {
		return ErrNoConnection
	}
	err = t.c.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("PRIVMSG #%s :%s", channelName, msg)))
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
func (t *Twitch) ReceiveOneMessage() (msg TwitchMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("receve failed %v", r)
		}
	}()
	t.Lock()
	defer t.Unlock()
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
	t.Lock()
	defer t.Unlock()
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
