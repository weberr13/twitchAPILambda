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
	// warewellMessages = []string{
	// 	"farewell %s, we will miss you! weberrSenaWave",
	// 	"%s left the stove on and has left the chat weberrSenaWave",
	// 	"bye %s, later gater weberrSenaWave",
	// 	"thanks for stopping by %s weberrSenaWave",
	// 	"adios %s weberrSenaWave",
	// 	"hasta la vista %s weberrSenaWave",
	// 	"%s may be trying to lurk but failed to keep the tab open weberrNoahCry",
	// }
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
		"gamers__lounge", "00rianaa",
		"nightbot", "kattah", "streamfahrer", "einfachuwe42", "aliceydra", "drapsnatt", "kofistreambot",
		"commanderroot", "zkeey", "lurxx", "fwost", "implium", "vlmercy", "rogueg1rl",
		"pokemoncommunitygame", "0ax2", "arctlco" /*maybe*/, "anotherttvviewer", "morgane2k7", "01aaliyah",
		"01ella", "own3d", "elbierro", "8hvdes", "7bvllet", "01olivia", "spofoh", "ahahahahhhhahahahahah", "d0nk7",
	}
}

// TrimBots from a user list
func TrimBots(users map[string]interface{}) {
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

// SuperShoutOut grabs clips
func (t *Twitch) SuperShoutOut(channelName string, user string, manual bool) []*TwithcClipInfo {
	user = strings.TrimPrefix(user, "@")
	user = strings.ToLower(user)
	alt := user
	if a, ok := AlternateUsers()[user]; ok {
		alt = a
	}

	userInfo, err := t.GetUserInfo(alt)
	if err != nil {
		log.Printf("could not get user info, not doing a shoutout: %s", err)
		return nil
	}
	// log.Printf("%#v", userInfo)
	chanInfo, err := t.GetChannelInfo(userInfo)
	if err != nil {
		log.Printf("could not get channel info, not doing a shoutout: %s", err)
		return nil
	}
	// log.Printf("*** channel info %#v", chanInfo)
	if chanInfo.GameName == "" || chanInfo.GameName == "<none>" {
		log.Printf("not shouting out user as they don't stream")
		return nil
	}
	followed, err := t.IFollowThem(userInfo.ID)
	if err != nil {
		log.Printf("something whent wrong getting follow info: %s", err)
		return nil
	}
	if !followed && !manual {
		log.Printf("no auto shoutout for: %#v", chanInfo)
		return nil
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
	clips, err := t.GetClips(userInfo)
	if err != nil {
		log.Printf("could not get clips: %s", err)
		return nil
	}
	return clips
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
	// user = strings.TrimPrefix(user, "@")
	// iBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(warewellMessages))))
	// messgeIndex := 0
	// if err == nil {
	// 	messgeIndex = int(iBig.Int64())
	// }
	// err = t.SendMessage(channelName, fmt.Sprintf(warewellMessages[messgeIndex], user))
	// if err != nil {
	// 	log.Printf("could not say goodbye to %s", user)
	// }
}

// Twitch talks to twitch
type Twitch struct {
	c            *websocket.Conn
	p            *websocket.Conn
	cfg          *config.Configuration
	token        string
	hostUserInfo *TwitchUserInfo
	url          url.URL
	purl         url.URL
	reconLock    *sync.Mutex // double locks are possible >:(
	sync.RWMutex
}

// NewTwitch chat interface
func NewTwitch(cfg *config.Configuration) (*Twitch, error) {
	u := url.URL{Scheme: "wss", Host: cfg.GetChatWSS(), Path: "/"}
	pubSubURL := url.URL{Scheme: "wss", Host: cfg.GetPubSubWSS(), Path: "/"}
	t := &Twitch{
		cfg:       cfg,
		url:       u,
		purl:      pubSubURL,
		reconLock: &sync.Mutex{},
	}
	return t, nil
}

// Open the connection again
func (t *Twitch) Open(ctx context.Context) error {
	t.Lock()
	defer t.Unlock()
	if t.c != nil {
		return nil
	}
	log.Printf("dialing chat")
	c, _, err := websocket.DefaultDialer.Dial(t.url.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to reach chat %w", err)
	}
	t.c = c
	log.Printf("setting chat options")

	err = t.setChatOps()
	if err != nil {
		log.Printf("could not set chat ops: %s", err)
		return err
	}

auth:
	for {
		log.Printf("attempting to authenticate chat")
		err = t.authenticate("weberr13bot", t.token) // twitch seems to ignore this and set it to whatever the user running the bot used to auth
		if err == ErrAuthFailed {
			log.Printf("forcing token reauth")
			err = t.cfg.InvalidateToken(ctx, t.cfg.Twitch.ChannelID, t.cfg.Twitch.ChannelName)
			if err != nil {
				log.Printf("could not invalidate old token: %s", err)
				return err
			}
			log.Printf("re-fetching auth token")

			tr, err := t.cfg.GetAuthTokenResponse(ctx, t.cfg.Twitch.ChannelID, t.cfg.Twitch.ChannelName)
			if err != nil {
				log.Printf("could not get auth token %s", err)
				return err
			}
			t.token = tr.Token

			continue
		}
		break auth
	}

	log.Printf("joining chat")
	err = t.joinChannels(t.cfg.Twitch.ChannelName)
	if err != nil {
		return fmt.Errorf("could not join channel on twitch: %w", err)
	}

	log.Printf("dialing pubsub")
	pub, _, err := websocket.DefaultDialer.Dial(t.purl.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to reach pubsub %w", err)
	}
	pub.SetPingHandler(func(s string) error {
		return nil
	})
	pub.SetPongHandler(func(s string) error {
		return nil
	})
	t.p = pub

	log.Printf("listening to pubsub topics")
	err = t.listenToTopics()
	if err != nil {
		t.p.Close()
		t.p = nil
		return fmt.Errorf("failed to listen to topics %w", err)
	}

	// err = t.sendMessagePrivate(t.cfg.Twitch.ChannelName, "weberr13bot bot has joined")
	// if err != nil {
	// 	return fmt.Errorf("could not join channel on twitch: %s", err)
	// }
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
	IsLive                     bool     `json:"is_live"`
	Title                      string   `json:"title"`
	Delay                      int      `json:"delay"`
	Taghs                      []string `json:"tags"`
	ContentClassificatinLables []string `json:"content_classification_labels"`
	ISBrandedContent           bool     `json:"is_branded_content"`
}

// TwithcClipInfo get clip response
type TwithcClipInfo struct {
	ID        string `json:"id"`        // : "AwkwardHelplessSalamanderSwiftRage",
	URL       string `json:"url"`       // : "https://clips.twitch.tv/AwkwardHelplessSalamanderSwiftRage",
	EmbeddURL string `json:"embed_url"` // : "https://clips.twitch.tv/embed?clip=AwkwardHelplessSalamanderSwiftRage",
	//	"broadcaster_id": "67955580",
	//	"broadcaster_name": "ChewieMelodies",
	//	"creator_id": "53834192",
	//	"creator_name": "BlackNova03",
	//	"video_id": "205586603",
	//	"game_id": "488191",
	//	"language": "en",
	Title        string    `json:"title"`         //	"title": "babymetal",
	ViewCount    int       `json:"view_count"`    // : 10,
	CreatedAt    time.Time `json:"created_at"`    // : "2017-11-30T22:34:18Z",
	ThumbnailURL string    `json:"thumbnail_url"` //: "https://clips-media-assets.twitch.tv/157589949-preview-480x272.jpg",
	Duration     float64   `json:"duration"`      //: 28.3
	//"vod_offset": 480
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

// GetClips gets the channel clips
func (t *Twitch) GetClips(userInfo *TwitchUserInfo) ([]*TwithcClipInfo, error) {
	if userInfo == nil || userInfo.ID == "" {
		return nil, fmt.Errorf("no user specified")
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.twitch.tv/helix/clips?broadcaster_id="+userInfo.ID, nil)
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
		Data []*TwithcClipInfo `json:"data"`
	}
	chanInfo := &respData{}
	err = json.Unmarshal(b, chanInfo)
	if err != nil {
		return nil, err
	}
	if len(chanInfo.Data) > 0 {
		return chanInfo.Data, nil
	}
	return nil, fmt.Errorf("user %#v not found", userInfo)
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

// // SearchChannels search live channels for a term
// func (t *Twitch) SearchChannels(term string) ([]TwitchChannelInfo, int, error) {
// 	params := url.Values{}
// 	// params.Add("live_only", "true")
// 	params.Add("first", "100")
// 	params.Add("query", term)
// 	reqString := "https://api.twitch.tv/helix/search/channels?" + params.Encode()
// 	log.Printf("searching for channels with %s", reqString)

// 	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
// 	defer cancel()
// 	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqString, nil)
// 	if err != nil {
// 		return nil, http.StatusInternalServerError, fmt.Errorf("cannot make request: %w", err)
// 	}
// 	t.authorizeRequest(req)
// 	res, err := http.DefaultClient.Do(req)
// 	if err != nil {
// 		return nil, http.StatusInternalServerError, fmt.Errorf("cannot do request: %w", err)
// 	}
// 	if res.Body != nil {
// 		defer res.Body.Close()
// 	}
// 	if res.StatusCode > http.StatusMultipleChoices {
// 		return nil, res.StatusCode, fmt.Errorf("got back %d on get streams command", res.StatusCode)
// 	}
// 	b, err := io.ReadAll(res.Body)
// 	if err != nil {
// 		return nil, http.StatusInternalServerError, err
// 	}
// 	log.Printf("got back: %s", string(b))
// 	type respData struct {
// 		Data []TwitchChannelInfo `json:"data"` // TODO: do we want this to be the real thing?
// 	}
// 	streams := &respData{}
// 	err = json.Unmarshal(b, streams)
// 	if err != nil {
// 		return nil, http.StatusInternalServerError, err
// 	}
// 	if len(streams.Data) == 0 {
// 		log.Printf("found nothing")
// 	}
// 	cleaned := []TwitchChannelInfo{}
// 	for _, d := range streams.Data {
// 		if d.IsLive {
// 			cleaned = append(cleaned, d)
// 		}
// 	}
// 	return cleaned, http.StatusOK, nil
// }

// GetAllStreamInfoForUsers will give the stream info for the given channel names
// curl -X GET 'https://api.twitch.tv/helix/streams'
// https://dev.twitch.tv/docs/api/reference/#get-streams
func (t *Twitch) GetAllStreamInfoForUsers(usernames ...string) (map[string]TwitchStreamInfo, int, error) {
	m := make(map[string]TwitchStreamInfo)
	if len(usernames) < 1 {
		return m, http.StatusInternalServerError, nil
	}
	if len(usernames) > 100 {
		return nil, http.StatusInternalServerError, fmt.Errorf("twitch API limits requests to 100 users at a time")
	}
	reqString := "https://api.twitch.tv/helix/streams"
	for i, user := range usernames {
		if i == 0 {
			reqString += "?"
		} else {
			reqString += "&"
		}
		reqString += "user_login=" + strings.ToLower(user)
	}
	reqString += fmt.Sprintf("&first=%d", len(usernames)) // limit is 100 this will need to page eventually
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqString, nil)
	if err != nil {
		return m, http.StatusInternalServerError, fmt.Errorf("cannot make request: %w", err)
	}
	t.authorizeRequest(req)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return m, http.StatusInternalServerError, fmt.Errorf("cannot do request: %w", err)
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode > http.StatusMultipleChoices {
		return m, res.StatusCode, fmt.Errorf("got back %d on get streams command", res.StatusCode)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return m, http.StatusInternalServerError, err
	}
	type respData struct {
		Data []TwitchStreamInfo `json:"data"` // TODO: do we want this to be the real thing?
	}
	streams := &respData{}
	err = json.Unmarshal(b, streams)
	if err != nil {
		return m, http.StatusInternalServerError, err
	}
	for _, stream := range streams.Data {
		m[stream.UserLogin] = stream
	}
	return m, http.StatusInternalServerError, nil
}

// ListenTopic describes a pubsub listen topic
type ListenTopic struct {
	Topics    []string `json:"topics"`
	AuthToken string   `json:"auth_token"`
}

// ListenMessage subscribes to a pubsub topic
type ListenMessage struct {
	Type  string      `json:"type"`
	Nonce string      `json:"nonce,omitempty"`
	Data  ListenTopic `json:"data"`
}

func (t *Twitch) listenToTopics() error {
	if t.p != nil {
		topics := []string{
			fmt.Sprintf("channel-bits-events-v1.%s", t.cfg.Twitch.ChannelID),
			fmt.Sprintf("channel-points-channel-v1.%s", t.cfg.Twitch.ChannelID),
			fmt.Sprintf("channel-subscribe-events-v1.%s", t.cfg.Twitch.ChannelID),
			fmt.Sprintf("channel-subscribe-events-v1.%s", t.cfg.Twitch.ChannelID),
		}
		msg := ListenMessage{
			Type: "LISTEN",
			Data: ListenTopic{
				AuthToken: t.token,
				Topics:    topics,
			},
		}
		err := t.p.WriteJSON(msg)
		if err != nil {
			return fmt.Errorf("could not write subscribe request %w", err)
		}
		t, b, err := t.p.ReadMessage()
		/// parse this???
		// 		2023/09/17 19:09:53 got 1 {"type":"RESPONSE","error":"","nonce":""}
		// : %!s(<nil>) subscribing to topics

		log.Printf("got %d %s: %s subscribing to topics", t, string(b), err)
		return nil
	}
	return ErrNoConnection
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
	if t.p != nil {
		err = t.p.Close()
		t.p = nil
	}
	return err
}

// setChatOps configures the chat session for twitch streams
func (t *Twitch) setChatOps() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("SetChatOps failed %v", r)
		}
	}()
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

// Reconnect when the token expires
func (t *Twitch) Reconnect(ctx context.Context, channelID, channelName string) error {
	t.reconLock.Lock()
	defer t.reconLock.Unlock()
	log.Printf("attempting to reconnect to twitch")
	log.Printf("attempting to close socket")
	err := t.Close()
	if err != nil {
		log.Printf("closing twitch failed: %s", err)
		return err
	}
	log.Printf("attempting to authenticate")
	err = t.GetAuthTokens(ctx, channelID, channelName)
	if err == config.ErrNeedAuthorization {
		return err
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return err
	}
	log.Printf("attempting to reopen socket")
	err = t.Open(ctx)
	if err != nil {
		log.Printf("repening twitch failed: %s", err)
		return err
	}
	return nil
}

// GetAuthTokens will try very hard to get the authentication tokens
func (t *Twitch) GetAuthTokens(ctx context.Context, channelID, channelName string) (err error) {
	var tr *config.TokenResponse
	tr, err = t.cfg.GetAuthTokenResponse(ctx, channelID, channelName)
	if err == config.ErrNeedAuthorization {
		retries := 20
	retry:
		for ; retries > 0; retries-- {
			log.Printf("failed to get auth token")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(30 * time.Second)
			tr, err = t.cfg.GetAuthTokenResponse(ctx, channelID, channelName)
			if err == nil {
				break retry
			} else if err != config.ErrNeedAuthorization {
				return err
			}
		}
	}
	if err != nil {
		log.Printf("could not get auth token %s", err)
		return err
	}
	t.token = tr.Token
	return err
}

// authenticate to twitch for the bot name with the given auth token
func (t *Twitch) authenticate(name, token string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authenticate failed %v", r)
		}
	}()
	if t.c == nil {
		return ErrNoConnection
	}
	if token == "" {
		return fmt.Errorf("no valid token provided")
	}
	if name == "" {
		name = "weberr13bot" // twitch seems to ignore this and set it to whatever the user running the bot used to auth
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

// joinChannels on an authenticated session
func (t *Twitch) joinChannels(channels ...string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("join failed %v", r)
		}
	}()
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
	log.Printf("joined %s", channelNames)
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
	return t.sendMessagePrivate(channelName, msg)
}

func (t *Twitch) sendMessagePrivate(channelName, msg string) (err error) {
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

func ctxSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TwitchEventMessage from the pubsub
type TwitchEventMessage struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

// TwitchPointRedemption from the pubsub
type TwitchPointRedemption struct {
	Timestamp  time.Time `json:"timestamp"`
	Redemption struct {
		ID   string `json:"id"`
		User struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"user"`
		ChannelID  string    `json:"channel_id"`
		RedeemedAt time.Time `json:"redeemed_at"`
		Reward     struct {
			ID        string `json:"id"`
			ChannelID string `json:"channel_id"`
			Title     string `json:"title"`
			// "prompt": "cleanside's finest \n",
			// "cost": 10,
			// "is_user_input_required": true,
			// "is_sub_only": false,
			// "image": {
			// "url_1x": "https://static-cdn.jtvnw.net/custom-reward-images/30515034/6ef17bb2-e5ae-432e-8b3f-5ac4dd774668/7bcd9ca8-da17-42c9-800a-2f08832e5d4b/custom-1.png",
			// "url_2x": "https://static-cdn.jtvnw.net/custom-reward-images/30515034/6ef17bb2-e5ae-432e-8b3f-5ac4dd774668/7bcd9ca8-da17-42c9-800a-2f08832e5d4b/custom-2.png",
			// "url_4x": "https://static-cdn.jtvnw.net/custom-reward-images/30515034/6ef17bb2-e5ae-432e-8b3f-5ac4dd774668/7bcd9ca8-da17-42c9-800a-2f08832e5d4b/custom-4.png"
			// },
			// "default_image": {
			// "url_1x": "https://static-cdn.jtvnw.net/custom-reward-images/default-1.png",
			// "url_2x": "https://static-cdn.jtvnw.net/custom-reward-images/default-2.png",
			// "url_4x": "https://static-cdn.jtvnw.net/custom-reward-images/default-4.png"
			// },
			// "background_color": "#00C7AC",
			// "is_enabled": true,
			// "is_paused": false,
			// "is_in_stock": true,
			// "max_per_stream": { "is_enabled": false, "max_per_stream": 0 },
			// "should_redemptions_skip_request_queue": true
		} `json:"reward"`
		UserInput string `json:"user_input"`
		Status    string `json:"status"`
	} `json:"redemption"`
}

// StartPubSubEventHandler is the event loop for twitch pub-sub
func (t *Twitch) StartPubSubEventHandler(ctx context.Context, wg *sync.WaitGroup,
	redemptionHandlers map[string]func(context.Context, TwitchPointRedemption),
) {
	wg.Add(1)
	log.Printf("starting pubsub event handler")
	go func() {
		defer wg.Done()

		for {
			if ctx.Err() != nil {
				log.Printf("shutting down")
				return
			}
			log.Printf("reading pubsub")
			msg, err := t.recieveOnePubSub()
			if err != nil {
				switch err {
				case ErrNoConnection:
					err := t.Reconnect(ctx, t.cfg.Twitch.ChannelID, t.cfg.Twitch.ChannelName)
					if err != nil {
						log.Printf("got error %s", err.Error())
						if ctxSleep(ctx, 1*time.Second) != nil {
							return
						}
					}
				default:
					log.Printf("got error %s", err.Error())
					if ctxSleep(ctx, 1*time.Second) != nil {
						return
					}
				}
				continue
			}
			if !config.IsLive.Load() {
				continue
			}
			switch msg.Type {
			case "MESSAGE":
				mmsg := TwitchEventMessage{}
				topic, ok := msg.Data["topic"].(string)
				if ok {
					log.Printf("got message for topic %s", topic)
				}
				msgData, ok := msg.Data["message"].(string)
				if !ok {
					log.Printf("unexpected event message: %#v", msg.Data)
					continue
				}
				err := json.Unmarshal([]byte(msgData), &mmsg)
				if err != nil {
					log.Printf("unexpected event message: %#v %s", msg.Data, err)
					continue
				}
				switch mmsg.Type {
				case "reward-redeemed":
					b, _ := json.Marshal(mmsg.Data)
					redemption := &TwitchPointRedemption{}
					err := json.Unmarshal(b, redemption)
					if err != nil {
						log.Printf("could not parse redemption")
						continue
					}
					f, ok := redemptionHandlers[redemption.Redemption.Reward.Title]
					if ok {
						f(ctx, *redemption)
					} else {
						log.Printf("got unhandled redemption %#v", redemption)
					}
				default:
					log.Printf("got message unknown %#v", mmsg)
				}
				continue
			case "PONG":
				continue
			case "RECONNECT":
				log.Printf("got reconnect message")
				err := t.Reconnect(ctx, t.cfg.Twitch.ChannelID, t.cfg.Twitch.ChannelName)
				if err != nil {
					log.Printf("got error %s", err.Error())
					if ctxSleep(ctx, 1*time.Second) != nil {
						return
					}
				}
			default:
				log.Printf("got PubSub unknown message %#v", *msg)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(120 * time.Second):
				msg := TwitchPubSubMessage{Type: "PING"}
				if t.p != nil {
					err := t.p.WriteJSON(msg)
					if err != nil {
						log.Printf("could not send ping %s", err)
						continue
					}
				}
			}
		}
	}()
}

// recieveOnePubSub gets a message from the pub/sub event channel
func (t *Twitch) recieveOnePubSub() (msg *TwitchPubSubMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("receve on pubsub failed %v", r)
		}
	}()
	msg = &TwitchPubSubMessage{}
	if t.p == nil {
		return nil, ErrNoConnection
	}
	msgType, b, err := t.p.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("could not get message %w", err)
	}
	switch msgType {
	case websocket.TextMessage:
		err = json.Unmarshal(b, &msg)
		return msg, err
	default:
		return nil, fmt.Errorf("got unexpected msg type: %d, %s", msgType, string(b))
	}
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

// TwitchPubSubMessage is a parsed message from twitch event pub/sub
type TwitchPubSubMessage struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
}
