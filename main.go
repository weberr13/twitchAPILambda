package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt"
	"github.com/morelj/lambada"
	"github.com/weberr13/twitchAPILambda/config"
	"github.com/weberr13/twitchAPILambda/tokenstore"
	twitchapi "github.com/weberr13/twitchAPILambda/twitch"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
)

var (
	ourConfig    *config.Configuration
	scopes       = []string{"clips:edit"}
	oauth2Config *oauth2.Config
)

func init() {
	ourConfig = config.NewConfig()
	tokenstore.Init(ourConfig)
}

func runCommand(w http.ResponseWriter, r *http.Request, cmd, name, id, token string) error {
	switch cmd {
	case "clip":
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, fmt.Sprintf("https://api.twitch.tv/helix/clips?broadcaster_id=%s", id), nil)
		if err != nil {
			log.Print("no request")
			_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		}
		req.Header.Add("Authorization", "Bearer "+token)
		req.Header.Add("Client-Id", ourConfig.ClientID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Print("no request")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error": "internal server error request %s"}`, err)))
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			idN, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				idN = 0
			}
			tokenstore.DeleteToken(r.Context(), name, int(idN))
			return HandleLogin(w, r)
		}
		m := twitchapi.CreateClipResponse{}
		b, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(b, &m)
		if err != nil {
			log.Print("no request")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error": "internal server error unmarshal and read%s"}`, err)))
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		}
		if resp.StatusCode >= http.StatusMultiStatus {
			m2 := twitchapi.APIError{}
			err = json.Unmarshal(b, &m2)
			if err != nil {
				log.Printf("bad response")
				_, _ = w.Write(([]byte)(fmt.Sprintf("ERROR: unexpected result: %s", string(b))))
				w.WriteHeader(resp.StatusCode)
				return nil
			}
			r := fmt.Sprintf("something went wrong: %s", m2.Message)
			_, _ = w.Write([]byte(r))
			w.WriteHeader(resp.StatusCode)
			return nil
		}
		r := ""
		for _, d := range m.Data {
			if d.EditURL != "" {
				r += fmt.Sprintf("successfully created clip with id:%s and url:%s. Use the URL to adjust timing and duration.", d.ID, d.EditURL)
			}
		}
		if r != "" {
			_, _ = w.Write([]byte(r))
			w.WriteHeader(http.StatusAccepted)
			return nil
		}

		r = fmt.Sprintf("unexpected response from TwitchAPI: %s", string(b))
		_, _ = w.Write([]byte(r))
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmt.Sprintf("I don't know how to %s", cmd)))
	}
	return nil
}

// HandleLogin is a Handler that redirects the user to Twitch for login, and provides the 'state'
// parameter which protects against login CSRF.
func HandleLogin(w http.ResponseWriter, r *http.Request) error {
	channel := r.Header.Get("Nightbot-Channel")
	cmd := ""
	if channel != "" {
		vals := strings.Split(channel, "&")
		id := ""
		name := ""
		userLevel := ""
		// displayName=weberr13&provider=twitch
		for _, v := range vals {
			switch {
			case strings.HasPrefix(v, "providerId="):
				id = strings.TrimPrefix(v, "providerId=")
			}
		}
		channel := r.Header.Get("Nightbot-User")
		//"name=weberr13&displayName=weberr13&provider=twitch&providerId=403503512&userLevel=moderator"
		vals = strings.Split(channel, "&")
		for _, v := range vals {
			switch {
			case strings.HasPrefix(v, "userLevel="):
				userLevel = strings.TrimPrefix(v, "userLevel=")
			case strings.HasPrefix(v, "name="):
				name = strings.TrimPrefix(v, "name=")
			}
		}
		level, ok := ourConfig.AuthorizedChannels[id]
		if !ok {
			log.Printf("id %s not found in %#v ", id, ourConfig.AuthorizedChannels)
			_, _ = w.Write([]byte(fmt.Sprintf("channel %s not authorized to use this plugin, contact weberr13 directly", id)))
			w.WriteHeader(http.StatusUnauthorized)
			return fmt.Errorf("not authorized to use this plugin, contact weberr13 directly")
		}
		levelN, ok := config.LevelAsNumber[strings.ToLower(userLevel)]
		if !ok {
			_, _ = w.Write([]byte("unexpected user level, contact weberr13"))
			w.WriteHeader(http.StatusUnauthorized)
			return fmt.Errorf("unexpected user level, contact weberr13")
		}
		if levelN > config.LevelAsNumber[ourConfig.AuthorizedChannels[id]] {
			_, _ = w.Write([]byte(fmt.Sprintf("Not authorized to use this plugin at %s, must be greater than or equal to %s, contact weberr13 directly", level, ourConfig.AuthorizedChannels[id])))
			w.WriteHeader(http.StatusUnauthorized)
			return fmt.Errorf("not authorized to use this plugin at %s, must be greater than or equal to %s, contact weberr13 directly", level, ourConfig.AuthorizedChannels[id])
		}
		cmd = r.URL.Query().Get("cmd")
		idN, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			idN = 0
		}
		token := tokenstore.GetToken(r.Context(), name, int(idN))
		if token == "" {
			_, _ = w.Write([]byte(fmt.Sprintf(`Please authorize or re-authorize the app by vistiting %s?name=%s&channel=%s`, ourConfig.OurURL, name, id)))
			w.WriteHeader(http.StatusUnauthorized)
			return nil
		}
		return runCommand(w, r, cmd, name, id, token)
	}
	token := jwt.New(jwt.SigningMethodHS256)
	claims := token.Claims.(jwt.MapClaims)
	name := r.URL.Query().Get("name")
	id := r.URL.Query().Get("channel")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("please specify the twitch username for authorization"))
		return nil
	}
	claims["channelID"] = id
	claims["name"] = name
	rbytes := make([]byte, 127)
	_, _ = rand.Read(rbytes)
	claims["rand"] = string(rbytes)
	log.Printf("jwt is %#v claims: %#v", token, claims)

	tokenString, err := token.SignedString([]byte(ourConfig.SignSecret))
	if err != nil {
		log.Printf("failure to sign")
		return fmt.Errorf("cannot sign %w", err)
	}

	log.Printf("redirecting to: " + oauth2Config.AuthCodeURL(tokenString))
	http.Redirect(w, r, oauth2Config.AuthCodeURL(tokenString), http.StatusTemporaryRedirect)
	log.Printf("state is %s", tokenString)

	return nil
}

// HumanReadableWrapper makes oauth returns readable
type HumanReadableWrapper struct {
	ToHuman string
	Code    int
	error
}

// HumanError is a readable string
func (h HumanReadableWrapper) HumanError() string { return h.ToHuman }

// HTTPCode is a response code
func (h HumanReadableWrapper) HTTPCode() int { return h.Code }

// AnnotateError wraps an error with a message that is intended for a human end-user to read,
// plus an associated HTTP error code.
func AnnotateError(err error, annotation string, code int) error {
	if err == nil {
		return nil
	}
	return HumanReadableWrapper{ToHuman: annotation, error: err}
}

// HandleOAuth2Callback is a Handler for oauth's 'redirect_uri' endpoint;
// it validates the state token and retrieves an OAuth token from the request parameters.
func HandleOAuth2Callback(w http.ResponseWriter, r *http.Request) (jwt.MapClaims, *oauth2.Token, error) {
	encodedState := r.FormValue("state")
	if encodedState == "" {
		log.Printf("couldn't find state in %#v", r)
		return nil, nil, fmt.Errorf("no state?")
	}
	token, err := jwt.Parse(encodedState, func(token *jwt.Token) (interface{}, error) {
		return []byte(ourConfig.SignSecret), nil
	})
	if err != nil {
		log.Printf("couldn't parse state in %#v", r)
		return nil, nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		log.Printf("couldn't find claims in state state in %#v", r)
		return nil, nil, fmt.Errorf("bad")
	}

	authToken, err := oauth2Config.Exchange(r.Context(), r.FormValue("code"))
	if err != nil {
		log.Printf("couldn't get auth from code %#v", r)
		return nil, nil, err
	}

	return claims, authToken, nil
}

func main() {
	// Gob encoding for gorilla/sessions
	gob.Register(&oauth2.Token{})

	oauth2Config = &oauth2.Config{
		ClientID:     ourConfig.ClientID,
		ClientSecret: ourConfig.ClientSecret,
		Scopes:       scopes,
		Endpoint:     twitch.Endpoint,
		RedirectURL:  ourConfig.RedirectURL,
	}
	lambada.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/redirect"):
			log.Print("doing redirect")
			claims, token, err := HandleOAuth2Callback(w, r)
			if err != nil {
				log.Print("oauth failed")
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error": "internal server error callback %s"}`, err)))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			name, ok := claims["name"].(string)
			id, okid := claims["channelID"].(string)
			channelID := int64(0)
			if okid {
				channelID, err = strconv.ParseInt(id, 10, 64)
				if err != nil {
					channelID = 0
				}
			}
			if ok && name != "" {
				err := tokenstore.PutToken(r.Context(), name, token.AccessToken, int(channelID))
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(fmt.Sprintf("failure to store token %s", err)))
					return
				}
				log.Print("oauth successful")
			} else {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("invalid authentication request, did you include your name and channel?"))
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("Success!  You can close this window"))
		default:
			log.Print("this is not a redirect")
			err := HandleLogin(w, r)
			if err != nil {
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error": "internal server error login %s"}`, err)))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			return
		}
	}))
}
