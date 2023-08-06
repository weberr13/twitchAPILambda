package pcg

import (
	"strings"

	"github.com/weberr13/twitchAPILambda/chat"
)

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
