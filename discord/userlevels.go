package discord

import (
	"fmt"
	"math"
	"sync"
)

var userlevelPrefix = "userlevel-"

// UserWithLevel is a description of a user's exp
type UserWithLevel struct {
	CurrentExp int     `json:"experience"`
	Name       string  `json:"name"`
	Server     string  `json:"sever"`
	Multiplier float64 `json:"multiplier"`
	sync.RWMutex
}

// NewUser creates a brand new user level tracker
func NewUser(server, name string) *UserWithLevel {
	u := &UserWithLevel{
		Name:       name,
		Multiplier: 1.0,
		CurrentExp: 0,
		Server:     server,
	}
	return u
}

// UpdateMultiplier allows multiplier to be changed for some good reason TBD
func (u *UserWithLevel) UpdateMultiplier(m float64) {
	u.Lock()
	defer u.Unlock()
	if m < 0 {
		return
	}
	if m > 2 {
		u.Multiplier = 2
		return
	}
	u.Multiplier = m
}

// Key for storage
func (u *UserWithLevel) Key() string {
	u.RLock()
	defer u.RUnlock()
	return fmt.Sprintf("%s%s%s", userlevelPrefix, u.Server, u.Name)
}

// Level according to exp
func (u *UserWithLevel) Level() int {
	u.RLock()
	defer u.RUnlock()
	switch {
	case u.CurrentExp < 100:
		return 1
	case u.CurrentExp < 200:
		return 2
	case u.CurrentExp < 400:
		return 3
	case u.CurrentExp < 600:
		return 4
	case u.CurrentExp < 900:
		return 5
	default:
		return 6
	}
}

// AddExp adds adjsted exp to user
func (u *UserWithLevel) AddExp(e int) {
	u.Lock()
	defer u.Unlock()
	u.CurrentExp += int(math.Ceil(float64(e) * (u.Multiplier)))
}
