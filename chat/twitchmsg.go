package chat

import (
	"fmt"
	"log"
	"strings"
)

// ErrInvalidMsg the message didn't parse
var ErrInvalidMsg = fmt.Errorf("invalid message, did not parse")

const (
	// UnknownType of message
	UnknownType = iota
	// PrivateMessage is a standard chat
	PrivateMessage
	// JoinMessage is something related to who is in the channel?
	JoinMessage
	// PingMessage is the Ping request from the server
	PingMessage
	// PartMessage is something related to Join?
	PartMessage
	// UserStateMessage is something we get at our join?
	UserStateMessage
	// RoomStateMessage tells us about the rules of the room
	RoomStateMessage
	// GlobalUserState ???
	GlobalUserState
	// Capacity ??
	Capacity
	// Authentication response
	Authentication
	// AuthenticationFail response
	AuthenticationFail
)

// TwitchMessage wrapper for parsing and providing info on individual chat messages
type TwitchMessage struct {
	t           int
	raw         string
	tags        string
	user        string
	users       map[string]string
	msg         string
	displayname string
	preambleKV  map[string]string
}

// TODO parse the preambles into a map since it is all k=v

func (c *TwitchMessage) parsePreamble() {
	// @badge-info=;
	// badges=vip/1;
	// color=#FF0000;
	// display-name=PokemonCommunityGame;
	// emotes=;
	// first-msg=0;
	// flags=;
	// id=7758397c-a244-40e2-82f1-68041d977aba;
	// mod=0;
	// returning-chatter=0;
	// room-id=403503512;
	// subscriber=0;
	// tmi-sent-ts=1679277344587;
	// turbo=0;
	// user-id=519435394;
	// user-type=;
	// vip=1 :pokemoncommunitygame!pokemoncommunitygame@pokemoncommunitygame.tmi.twitch.tv PRIVMSG #weberr13 :

	if !strings.HasPrefix(c.raw, "@") {
		return
	}
	split := strings.SplitN(c.raw, " ", 2)
	if len(split) < 2 {
		return
	}
	c.preambleKV = map[string]string{}
	kvs := strings.Split(split[0][1:], ";")
	for _, kv := range kvs {
		k := strings.SplitN(kv, "=", 2)
		if len(k) == 2 {
			c.preambleKV[k[0]] = k[1]
		}
	}
	if n, ok := c.preambleKV["display-name"]; ok {
		c.displayname = n
	}
}

// Parse message from the websocket read
func (c *TwitchMessage) Parse(b []byte) error {
	if c.users == nil {
		c.users = map[string]string{}
	}
	c.raw = string(b)
	c.parsePreamble()
	switch {
	case strings.Contains(c.raw, "twitch.tv PRIVMSG #"):
		// DO THIS FIRST TO AVOID CLEVERLY DESIGNED MESSAGES IN BODY TO TRICK US!!!!
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
		splits = strings.SplitN(c.msg, ":", 2)
		if len(splits) == 2 {
			c.msg = splits[1]
		}

		return nil
	case strings.Contains(c.raw, "twitch.tv NOTICE * :Login authentication failed"):
		log.Printf("failed to authenticate")
		c.t = AuthenticationFail
		return nil
	case strings.Contains(c.raw, "twitch.tv USERSTATE #"):
		log.Printf("Send message sucess/userstate")
		// log.Print(c.raw)
		c.t = UserStateMessage
		// :@badge-info=subscriber/2;badges=broadcaster/1,subscriber/0,premium/1;color=;display-name=weberr13;emote-sets=0
		// ,19194,365424,772925,796420,1133584,300374282,301163440,301560802,302428010,302559488,302902342,303682600,304771063,311989890,3340003
		// 92,337514477,342546074,357197921,374574358,377755724,382147054,385039701,392057983,397660890,401437971,404889748,406514671,409329646,
		// 411795034,416546825,420879231,425962181,434204561,441066318,445747742,447013064,450374251,467961185,473039848,477339272,478656779,485
		// 479874,494916443,669648413,1026716137,1145663083,1983690061,0ef07359-598e-41c9-923b-7dcbdbf1c530,3c680c77-4eef-440b-b21f-f91a6516875e
		// ,684323cc-fa78-4bfe-8be5-2ef6af969f82,6aace4ef-b2b8-4755-a824-0b9683dbf24b,70a2d824-18c9-49f4-b05a-007cd9e74ad0,72883cf3-894b-415e-a7
		// c2-7a534f08248e,9cf067fb-8144-40e0-9516-79fc2e442b05,9e33fa98-b1e8-4593-9e68-30c8cac92ce4,a3d56025-447d-490e-b4db-8168d3f7c5a5,c0e9e8
		// 5b-6d1f-4bf7-8397-141fce19f987,e2a148df-2997-4bf3-978a-acc33fe2e9dd,e516804c-e5a3-4aa1-892c-c646004c4088;id=3d63aebc-642d-4d76-adf9-7
		// a3dacaa5267;mod=0;subscriber=1;user-type= :tmi.twitch.tv USERSTATE #weberr13
		return nil
	case strings.Contains(c.raw, "twitch.tv 001") &&
		strings.Contains(c.raw, "twitch.tv 002") &&
		strings.Contains(c.raw, "twitch.tv 003") &&
		strings.Contains(c.raw, "twitch.tv 004") &&
		strings.Contains(c.raw, "twitch.tv 375") &&
		strings.Contains(c.raw, "twitch.tv 372") &&
		strings.Contains(c.raw, "twitch.tv 376"):
		log.Printf("Authentication Sucesss")
		// log.Print(c.raw)
		c.t = Authentication
		// :tmi.twitch.tv 001 weberr13 :Welcome, GLHF!
		// :tmi.twitch.tv 002 weberr13 :Your host is tmi.twitch.tv
		// :tmi.twitch.tv 003 weberr13 :This server is rather new
		// :tmi.twitch.tv 004 weberr13 :-
		// :tmi.twitch.tv 375 weberr13 :-
		// :tmi.twitch.tv 372 weberr13 :You are in a maze of twisty passages, all alike.
		// :tmi.twitch.tv 376 weberr13 :>
		return nil
	case strings.Contains(c.raw, "twitch.tv CAP * ACK"):
		log.Print("Capactiy")
		c.t = Capacity
		return nil
	case strings.Contains(c.raw, "twitch.tv GLOBALUSERSTATE #"):
		log.Print("GlobalUserState")
		// log.Print(c.raw)
		// @badge-info=;badges=premium/1;color=;display-name=weberr13;emote-sets=0,19194,365424,772925,796420,1133584,300374282,301163440,301560
		// 802,302428010,302559488,302902342,303682600,304771063,311989890,334000392,337514477,342546074,357197921,374574358,377755724,382147054
		// ,385039701,392057983,397660890,401437971,404889748,406514671,409329646,411795034,416546825,420879231,425962181,434204561,441066318,44
		// 5747742,447013064,450374251,467961185,473039848,477339272,478656779,485479874,494916443,669648413,1026716137,1145663083,1983690061,0e
		// f07359-598e-41c9-923b-7dcbdbf1c530,3c680c77-4eef-440b-b21f-f91a6516875e,684323cc-fa78-4bfe-8be5-2ef6af969f82,6aace4ef-b2b8-4755-a824-
		// 0b9683dbf24b,70a2d824-18c9-49f4-b05a-007cd9e74ad0,72883cf3-894b-415e-a7c2-7a534f08248e,9cf067fb-8144-40e0-9516-79fc2e442b05,9e33fa98-
		// b1e8-4593-9e68-30c8cac92ce4,a3d56025-447d-490e-b4db-8168d3f7c5a5,c0e9e85b-6d1f-4bf7-8397-141fce19f987,e2a148df-2997-4bf3-978a-acc33fe
		// 2e9dd,e516804c-e5a3-4aa1-892c-c646004c4088;user-id=403503512;user-type= :tmi.twitch.tv GLOBALUSERSTATE
		c.t = GlobalUserState
		return nil
	case strings.Contains(c.raw, "twitch.tv ROOMSTATE #"):
		log.Print("RoomStateMessage")
		// log.Print(c.raw)
		// :weberr13!weberr13@weberr13.tmi.twitch.tv JOIN #weberr13
		// :weberr13.tmi.twitch.tv 353 weberr13 = #weberr13 :weberr13
		// :weberr13.tmi.twitch.tv 366 weberr13 #weberr13 :End of /NAMES list
		// @badge-info=subscriber/2;badges=broadcaster/1,subscriber/0,premium/1;color=;display-name=weberr13;emote-sets=0,19194,365424,772925,79
		// 6420,1133584,300374282,301163440,301560802,302428010,302559488,302902342,303682600,304771063,311989890,334000392,337514477,342546074,
		// 357197921,374574358,377755724,382147054,385039701,392057983,397660890,401437971,404889748,406514671,409329646,411795034,416546825,420
		// 879231,425962181,434204561,441066318,445747742,447013064,450374251,467961185,473039848,477339272,478656779,485479874,494916443,669648
		// 413,1026716137,1145663083,1983690061,0ef07359-598e-41c9-923b-7dcbdbf1c530,3c680c77-4eef-440b-b21f-f91a6516875e,684323cc-fa78-4bfe-8be
		// 5-2ef6af969f82,6aace4ef-b2b8-4755-a824-0b9683dbf24b,70a2d824-18c9-49f4-b05a-007cd9e74ad0,72883cf3-894b-415e-a7c2-7a534f08248e,9cf067f
		// b-8144-40e0-9516-79fc2e442b05,9e33fa98-b1e8-4593-9e68-30c8cac92ce4,a3d56025-447d-490e-b4db-8168d3f7c5a5,c0e9e85b-6d1f-4bf7-8397-141fc
		// e19f987,e2a148df-2997-4bf3-978a-acc33fe2e9dd,e516804c-e5a3-4aa1-892c-c646004c4088;mod=0;subscriber=1;user-type= :tmi.twitch.tv USERST
		// ATE #weberr13
		// @emote-only=0;followers-only=-1;r9k=0;room-id=403503512;slow=0;subs-only=0 :tmi.twitch.tv ROOMSTATE #weberr13
		c.t = RoomStateMessage
		return nil
	case strings.HasPrefix(c.raw, "PING :"):
		splits := strings.SplitN(c.raw, ":", 2)
		if len(splits) == 2 {
			c.msg = splits[1]
		} else {
			c.msg = "tmi.twitch.tv"
		}
		c.t = PingMessage
		return nil
	case strings.Contains(c.raw, "twitch.tv JOIN #"):
		log.Print("JoinMessage")
		for _, l := range strings.Split(c.raw, "\r\n") {
			if len(l) <= 2 {
				continue
			}
			splits := strings.SplitN(l[1:], "!", 2)
			if len(splits) == 2 {
				c.users[splits[0]] = strings.SplitN(splits[1], " ", 2)[0]
				c.t = JoinMessage
			} else {
				log.Printf(`could not parse "%s"`, l)
			}
		}

		// known bots:
		// :pokemoncommunitygame!pokemoncommunitygame@pokemoncommunitygame.tmi.twitch.tv JOIN #weberr13
		// :nightbot!nightbot@nightbot.tmi.twitch.tv JOIN #weberr13
		// :elbierro!elbierro@elbierro.tmi.twitch.tv JOIN #weberr13

		// suspected bots:
		// :aliceydra!aliceydra@aliceydra.tmi.twitch.tv JOIN #weberr13
		// :lurxx!lurxx@lurxx.tmi.twitch.tv JOIN #weberr13
		// :paradise_for_streamers!paradise_for_streamers@paradise_for_streamers.tmi.twitch.tv JOIN #weberr13
		// :01olivia!01olivia@01olivia.tmi.twitch.tv JOIN #weberr13
		// :lylituf!lylituf@lylituf.tmi.twitch.tv JOIN #weberr13
		// :commanderroot!commanderroot@commanderroot.tmi.twitch.tv JOIN #weberr13
		// :streamfahrer!streamfahrer@streamfahrer.tmi.twitch.tv JOIN #weberr13
		// :drapsnatt!drapsnatt@drapsnatt.tmi.twitch.tv JOIN #weberr13
		// :tacotuesday7313!tacotuesday7313@tacotuesday7313.tmi.twitch.tv JOIN #weberr13
		// :kattah!kattah@kattah.tmi.twitch.tv JOIN #weberr13
		// :einfachuwe42!einfachuwe42@einfachuwe42.tmi.twitch.tv JOIN #weberr13

		// real users
		// :da_panda06!da_panda06@da_panda06.tmi.twitch.tv JOIN #weberr13

		return nil
	case strings.Contains(c.raw, "twitch.tv PART #"):
		log.Print("PartMessage")
		for _, l := range strings.Split(c.raw, "\r\n") {
			if len(l) <= 2 {
				continue
			}
			splits := strings.SplitN(l[1:], "!", 2)
			if len(splits) == 2 {
				c.users[splits[0]] = strings.SplitN(splits[1], " ", 2)[0]
				c.t = PartMessage
			} else {
				log.Printf(`could not parse "%s"`, l)
			}
		}

		return nil

	case strings.Contains(c.raw, "login authentication failed"):
		log.Print("Authenticatin failed")
		log.Print(c.raw)
		c.t = AuthenticationFail
		return nil
	default:
		log.Print("unknown message:")
		log.Print(c.raw)
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

// Users found in Join/Part messages
func (c TwitchMessage) Users() map[string]string {
	tmp := map[string]string{}
	for k, v := range c.users {
		tmp[k] = v
	}
	return tmp
}

// String is readable
func (c TwitchMessage) String() string {
	// TODO switch on type, do different things for different messages
	return fmt.Sprintf("%s: %s", c.displayname, c.msg)
}

// GoString is verbose
func (c TwitchMessage) GoString() string {
	s := strings.Builder{}
	s.WriteString(fmt.Sprintf(`t: "%d"`, c.t))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`raw: "%s"`, c.raw))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`tags: "%s"`, c.tags))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`user: "%s"`, c.user))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`users: "%v"`, c.users))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`msg: "%s"`, c.msg))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`displayname: "%s"`, c.displayname))
	s.WriteString(",")
	s.WriteString(fmt.Sprintf(`preambleLV: "%v"`, c.preambleKV))
	s.WriteString(",")
	return s.String()
}

// DisplayName of the user who sent the message
func (c TwitchMessage) DisplayName() string {
	return c.displayname
}

// Raw message contents with headers/etc
func (c TwitchMessage) Raw() string {
	return c.raw
}

// Type of chat message
func (c TwitchMessage) Type() int {
	return c.t
}

// IsBotCommand will say if a chat message should be interpreted as a bot command
func (c TwitchMessage) IsBotCommand() bool {
	return strings.HasPrefix(c.msg, "!")
}

// IsMod for restricted bot commands
func (c TwitchMessage) IsMod() bool {
	if c.preambleKV["mod"] == "1" || c.IsOwner() {
		return true
	}
	return false
}

// IsOwner is the channel owner/broadcaster
func (c TwitchMessage) IsOwner() bool {
	return strings.Contains(c.preambleKV["badges"], "broadcaster/1")
}

// IsSub is a subscriber
func (c TwitchMessage) IsSub() bool {
	return c.preambleKV["subscriber"] == "1" || c.IsOwner()
}

// IsVIP are VIPs
func (c TwitchMessage) IsVIP() bool {
	return c.preambleKV["vip"] == "1" || c.IsOwner()
}

// GetBotCommand gets the command passed if IsBotCommand
func (c TwitchMessage) GetBotCommand() string {
	splits := strings.SplitN(c.msg, " ", 2)
	if len(splits) > 0 {
		return strings.TrimSpace(splits[0][1:])
	}
	return ""
}

// GetBotCommandArgs returns any arguments passed to the command as a single string
func (c TwitchMessage) GetBotCommandArgs() string {
	splits := strings.SplitN(c.msg, " ", 2)
	if len(splits) > 1 {
		return strings.TrimSpace(splits[1])
	}
	return ""
}
