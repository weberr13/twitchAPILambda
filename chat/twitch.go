package chat

import (
	"fmt"
	"log"
	"net/url"

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
)

// Bots we know about
func Bots() []string {
	return []string{
		"nightbot", "kattah", "streamfahrer", "einfachuwe42", "aliceydra", "drapsnatt",
		"commanderroot",
		"pokemoncommunitygame", "0ax2",
		"01ella", "own3d", "elbierro", "8hvdes", "7bvllet", "01olivia", "spofoh", "ahahahahhhhahahahahah",
	}
}

// TrimBots from a user list
func TrimBots(users map[string]string) {
	for _, bot := range Bots() {
		delete(users, bot)
	}
}

// Twitch talks to twitch
type Twitch struct {
	c *websocket.Conn
}

// NewTwitch chat interface
func NewTwitch(cfg *config.Configuration) (*Twitch, error) {
	u := url.URL{Scheme: "wss", Host: cfg.GetChatWSS(), Path: "/"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	return &Twitch{c: c}, nil
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

// SendMessage to a channel
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
