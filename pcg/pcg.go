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
