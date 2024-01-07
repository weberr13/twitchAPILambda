package pcg

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/weberr13/twitchAPILambda/chat"
	"github.com/weberr13/twitchAPILambda/db"
)

var dbPrefix = "wondertrade-"

// WonderTrade reminder info
type WonderTrade struct {
	Username string    `json:"username"`
	Deadline time.Time `json:"deadline"`
}

// NewWonderTradeReminder default to 3 hours from when created
func NewWonderTradeReminder(now time.Time, user string, override ...time.Duration) *WonderTrade {
	wt := &WonderTrade{
		Username: user,
	}
	duration := 3 * time.Hour
	if len(override) > 0 {
		duration = override[0]
	}
	wt.Deadline = now.Add(duration)
	return wt
}

// Key for persistent
func (w WonderTrade) Key() string {
	return fmt.Sprintf("%s%s", dbPrefix, w.Username)
}

// Ready to trade?
func (w WonderTrade) Ready(now time.Time) bool {
	return now.After(w.Deadline)
}

// HalfWay to wonder trade (1.5 hours)
func (w WonderTrade) HalfWay(now time.Time) bool {
	return now.Add(90*time.Minute).After(w.Deadline) && !now.Add(90*time.Minute).Add(30*time.Second).After(w.Deadline)
}

// Notifier sends stuff to the active place (probably twitch)
type Notifier interface {
	SendMessage(channelName, msg string) (err error)
}

// WonderTradeWatcher watches for wonder trades, notifies chat
type WonderTradeWatcher struct {
	persister db.Persister
	notifier  Notifier
	wg        *sync.WaitGroup
	done      chan struct{}
	sync.Mutex
}

// NewWonderTradeWatcher makes one
func NewWonderTradeWatcher(persister db.Persister, notifier Notifier) *WonderTradeWatcher {
	wtw := &WonderTradeWatcher{
		persister: persister,
		notifier:  notifier,
		wg:        &sync.WaitGroup{},
		done:      make(chan struct{}),
	}
	return wtw
}

// Run in the background
func (wtw *WonderTradeWatcher) Run(channelName string) {
	wtw.wg.Add(1)
	go func() {
		defer wtw.wg.Done()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				allTrades, err := wtw.persister.PrefixScan(dbPrefix)
				if err != nil {
					log.Printf("failed to get any wondertrades: %s", err)
					continue
				}
				for _, tradeKey := range allTrades {
					wt := &WonderTrade{}
					err := wtw.persister.Get(tradeKey, wt)
					if err != nil {
						log.Printf("got error on key %s: %s", tradeKey, err)
						continue
					}
					if wt.Ready(time.Now()) {
						_ = wtw.notifier.SendMessage(channelName, fmt.Sprintf("%s your wonder trade is ready! refresh with !wt [wait]", wt.Username))
						err := wtw.persister.Delete(tradeKey)
						if err != nil {
							log.Printf("could not delete wonder trade reminder %v: %s", wt, err)
						}
					} else if wt.HalfWay(time.Now()) {
						_ = wtw.notifier.SendMessage(channelName, fmt.Sprintf("%s half way to your wonder trade", wt.Username))
					}
				}
			case <-wtw.done:
				return
			}
		}
	}()
}

// Close things, idempotnent
func (wtw *WonderTradeWatcher) Close() error {
	wtw.Lock()
	defer wtw.Unlock()
	if wtw.done != nil {
		close(wtw.done)
		wtw.done = nil
		wtw.wg.Wait()
	}
	return nil
}

// IsSpawnCommand determines if the given message is a PCG spawn command
func IsSpawnCommand(msg chat.TwitchMessage) bool {
	if msg.DisplayName() == "PokemonCommunityGame" && strings.Contains(msg.Body(), "Catch it using !pokecatch (winners revealed in 90s)") {
		return true
	}
	return false
}

// CheckPokemon issues a pokemon check command
func CheckPokemon(channelName string, tw *chat.Twitch) error {
	return tw.SendMessage(channelName, "!pokecheck")
}

// CatchPokemon catch a pokemon automatically
func CatchPokemon(channelName string, tw *chat.Twitch, ball string) error {
	return tw.SendMessage(channelName, "!pokecatch "+ball)
}

// IsRegistered response message
func IsRegistered(msg chat.TwitchMessage) bool {
	// @weberr13 Sentret registered in Pokédex: ✔
	return msg.DisplayName() == "PokemonCommunityGame" && strings.Contains(msg.Body(), "registered in Pokédex")
}

// IsCaught checks if a pcg check command response indicates the user has the pokemon
func IsCaught(msg chat.TwitchMessage) bool {
	// @weberr13 Sentret registered in Pokédex: ✔
	if msg.DisplayName() == "PokemonCommunityGame" && strings.Contains(msg.Body(), "registered in Pokédex") {
		if strings.Contains(msg.Body(), "registered in Pokédex: ✔") {
			return true
		}
	}
	return false
}

// IsCaughtUser is the user who ran !pokecheck
func IsCaughtUser(msg chat.TwitchMessage) string {
	if msg.DisplayName() == "PokemonCommunityGame" && strings.Contains(msg.Body(), "registered in Pokédex") {
		split := strings.SplitN(msg.Body(), " ", 2)
		if len(split) != 2 {
			return ""
		}
		user := split[0][1:]
		if strings.Contains(msg.Body(), "registered in Pokédex: ✔") {
			return user
		}
	}
	return ""
}
