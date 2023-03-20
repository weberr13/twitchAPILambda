package kukoro

import (
	"strings"

	"github.com/weberr13/twitchAPILambda/chat"
)

// !getinfo
// ☺ACTION [KUKORO] WEBERR13 (Lv. 20, Df. 15%, Crit. 13%, Agi. 10%) >  [Damage x2 against enemy zombie after inflicting odd damage] and [Level +3 all your team if you die by enemy zombie].☺

// start
// ☺ACTION [KUKORO] RAID BEGINS >>> Let’s take down this dungeon!☺

// end
// ☺ACTION [KUKORO] RAID IS OVER >>> BOT-STEPHEN leveled up for the next raid.☺
// ☺ACTION [KUKORO] RAID IS OVER >>> All viewers have fainted!☺
func IsKukoroMsg(msg chat.TwitchMessage) bool {
	return strings.Contains(msg.Body(), "ACTION [KUKORO] ") 
}