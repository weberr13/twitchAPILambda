package config

import (
	_ "embed" // embed the config in the binary for now
	"encoding/json"
	"log"
	"strings"
)

//go:embed config.json
var configBytes []byte

// LevelAsNumber maps user levels to a number with owner == 0
var LevelAsNumber = map[string]int{
	"owner":      0,
	"moderator":  1,
	"vip":        2,
	"regular":    3,
	"subscriber": 4,
	"everyone":   5,
}

// Configuration embedded at build time
type Configuration struct {
	ClientSecret       string            `json:"clientSecret"`
	ClientID           string            `json:"clientID"`
	OurURL             string            `json:"ourURL"`
	TableName          string            `json:"tableName"`
	RedirectURL        string            `json:"redirect"`
	SignSecret         string            `json:"signSecret"`
	AuthorizedChannels map[string]string `json:"authorizedChannels"`
}

func ToNum(level string) int {
	n, ok := LevelAsNumber[strings.ToLower(level)]
	if !ok {
		return 6
	}
	return n
}

// NewConfig from the embedded json
func NewConfig() *Configuration {
	ourConfig := &Configuration{}
	err := json.Unmarshal(configBytes, ourConfig)
	if err != nil {
		log.Fatal(err)
	}
	if ourConfig.RedirectURL == "" {
		ourConfig.RedirectURL = ourConfig.OurURL + "redirect"
	}
	if ourConfig.SignSecret == "" {
		ourConfig.SignSecret = "simpleSecret"
	}
	log.Printf("config is %#v from %s", ourConfig, string(configBytes))
	for id, level := range ourConfig.AuthorizedChannels {
		if ToNum(level) > 4 {
			ourConfig.AuthorizedChannels[id] = "owner"
			log.Printf("overriding user level to owner for %s, must be at least a regular to use the commands", id)
		}
	}
	return ourConfig
}
