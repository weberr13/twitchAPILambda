package pcg

import (
	"strings"

	"github.com/weberr13/twitchAPILambda/chat"
)

func IsSpawnCommand(msg chat.TwitchMessage) bool {
	if msg.DisplayName() == "PokemonCommunityGame" && strings.Contains(msg.Body(), "Catch it using !pokecatch (winners revealed in 90s)") {
		return true
	}
	return false
}

func CheckPokemon(channelName string, tw *chat.Twitch) error {
	return tw.SendMessage(channelName, "!pokecheck")
}