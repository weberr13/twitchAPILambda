package chat

import (
	"fmt"
	"strings"
)

var (
	// ErrInvalidMsg the message didn't parse
	ErrInvalidMsg = fmt.Errorf("invalid message, did not parse")
)

const (
	UnknownType = iota
	PrivateMessage 
	JoinMessage
	PingMessage
)

type TwitchMessage struct {
	t int
	raw string
	tags string
	user string
	msg string
	displayname string
}

// Parse message from the websocket read
func (c *TwitchMessage) Parse(b []byte) error {
	c.raw = string(b)
	// do better than this hack in the future
	switch {
	case strings.HasPrefix(c.raw,"PING :"):
		splits := strings.SplitN(c.raw, ":",2)
		if len(splits) == 2 {
			c.msg = splits[1]
		} else {
			c.msg = "tmi.twitch.tv"
		}
		c.t = PingMessage
		return nil
	case strings.Contains(c.raw, "twitch.tv JOIN #"):
		c.t = JoinMessage
// pokemoncommunitygame!pokemoncommunitygame@pokemoncommunitygame.tmi.twitch.tv JOIN #weberr13
// :aliceydra!aliceydra@aliceydra.tmi.twitch.tv JOIN #weberr13
// :lurxx!lurxx@lurxx.tmi.twitch.tv JOIN #weberr13
// :paradise_for_streamers!paradise_for_streamers@paradise_for_streamers.tmi.twitch.tv JOIN #weberr13
// :01olivia!01olivia@01olivia.tmi.twitch.tv JOIN #weberr13
// :lylituf!lylituf@lylituf.tmi.twitch.tv JOIN #weberr13
// :nightbot!nightbot@nightbot.tmi.twitch.tv JOIN #weberr13
// :commanderroot!commanderroot@commanderroot.tmi.twitch.tv JOIN #weberr13
// :streamfahrer!streamfahrer@streamfahrer.tmi.twitch.tv JOIN #weberr13
// :drapsnatt!drapsnatt@drapsnatt.tmi.twitch.tv JOIN #weberr13
// :tacotuesday7313!tacotuesday7313@tacotuesday7313.tmi.twitch.tv JOIN #weberr13
// :kattah!kattah@kattah.tmi.twitch.tv JOIN #weberr13
// :da_panda06!da_panda06@da_panda06.tmi.twitch.tv JOIN #weberr13
// :einfachuwe42!einfachuwe42@einfachuwe42.tmi.twitch.tv JOIN #weberr13
// :elbierro!elbierro@elbierro.tmi.twitch.tv JOIN #weberr13
		return nil
	case strings.Contains(c.raw, "twitch.tv PRIVMSG #"): 
		c.t = PrivateMessage
// @badge-info=;badges=vip/1;color=#FF0000;display-name=PokemonCommunityGame;emotes=;first-msg=0;flags=;id=7758397c-a244-40e2-82f1-68041d977aba;mod=0;returning-chatter=0;room-id=403503512;subscriber=0;tmi-sent-ts=1679277344587;turbo=0;user-id=519435394;user-type=;vip=1 :pokemoncommunitygame!pokemoncommunitygame@pokemoncommunitygame.tmi.twitch.tv PRIVMSG #weberr13 :@weberr13 Sentret registered in Pokédex: ✔
		splits := strings.SplitN(c.raw, " ", 3)
		if len(splits) < 3 {
			return ErrInvalidMsg
		}
		c.tags = splits[0]
		c.user = splits[1]
		c.msg = splits[2]
	
		splits = strings.SplitN(c.user, "!", 2)
		if len(splits) == 2 {
			c.user = splits[0]
		}
		if c.tags[0] == '@' {
			c.tags = c.tags[1:]
		}
		splits = strings.Split(c.tags, ";")
		for _, split := range splits {
			kv := strings.SplitN(split, "=", 2)
			if len(kv) == 2 {
				switch kv[0] {
				case "display-name": 
					c.displayname = kv[1]
				}
			}
		}
		splits = strings.SplitN(c.msg, ":", 2)
		if len(splits) == 2 {
			c.msg = splits[1]
		}
	
		return nil
	}
	return nil
}

// Body of the message
func (c TwitchMessage) Body() string {
	return c.msg
}

// User that sent the message
func (c TwitchMessage) User() string {
	return c.user
}

// String is readable
func (c TwitchMessage) String() string {
	return fmt.Sprintf("%s: %s", c.displayname, c.msg)
}

func (c TwitchMessage) DisplayName() string {
	return c.displayname
}

func (c TwitchMessage) Raw() string {
	return c.raw
}

func (c TwitchMessage) Type() int {
	return c.t
}

func (c TwitchMessage) IsBotCommand() bool {
	return strings.HasPrefix(c.msg, "!")
}

func (c TwitchMessage) GetBotCommand() string {
	splits := strings.SplitN(c.msg, " ", 2)
	if len(splits) > 0 {
		return strings.TrimSpace(splits[0][1:])
	}
	return ""
}

func (c TwitchMessage) GetBotCommandArgs() string {
	splits := strings.SplitN(c.msg, " ", 2)
	if len(splits) > 1 {
		return strings.TrimSpace(splits[1])
	}
	return ""
}