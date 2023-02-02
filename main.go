package main

import (
	"context"
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
	"time"

	cache "github.com/alexions/go-lru-ttl-cache"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/golang-jwt/jwt"
	"github.com/morelj/lambada"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/twitch"
)

//go:embed config.json
var configBytes []byte

// Configuration embedded at build time
type Configuration struct {
	ClientSecret string `json:"clientSecret"`
	ClientID     string `json:"clientID"`
	OurURL       string `json:"ourURL"`
	TableName    string `json:"tableName"`
	RedirectURL  string `json:"redirect"`
	SignSecret   string `json:"signSecret"`
}

// CreateClipResponse is the response from twitch on creating a clip
type CreateClipResponse struct {
	Data []struct {
		EditURL string `json:"edit_url"`
		ID      string `json:"id"`
	} `json:"data"`
}

// TwitchAPIError is the standard error struct from twitch
type TwitchAPIError struct {
	Error   string `json:"error"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// TokenDB describes the table structure of stored tokens
type TokenDB struct {
	Name      string `json:"name" dynamodbav:"name"`
	Token     string `json:"token" dynamodbav:"token"`
	ChannelID int    `json:"channelId" dynamodbav:"channelid"`
}

var (
	ourConfig    = Configuration{}
	scopes       = []string{"clips:edit"}
	oauth2Config *oauth2.Config
	tokens       *dynamodb.Client
	tokenCache   *cache.LRUCache
)

func init() {
	sdkConfig, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal(err)
	}

	tokens = dynamodb.NewFromConfig(sdkConfig)
	cfg := cache.Configuration().SetMaxSize(1024).SetDefaultTTL(30 * time.Minute)
	tokenCache = cache.NewLRUCache(cfg)

	err = json.Unmarshal(configBytes, &ourConfig)
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
}

// nightbot header -> use a secret key
// no nightbot header -> generate a key and store secret

func putToken(ctx context.Context, name, token string, channelID int) error {
	todo := TokenDB{
		Name:      name,
		Token:     token,
		ChannelID: channelID,
	}

	item, err := attributevalue.MarshalMap(todo)
	if err != nil {
		return err
	}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(ourConfig.TableName),
		Item:      item,
	}

	_, err = tokens.PutItem(ctx, input)
	if err != nil {
		return err
	}
	log.Printf("caching token for %s %d", name, channelID)
	tokenCache.Set(fmt.Sprintf("%s:%d", name, channelID), token)
	return nil
}

func deleteToken(ctx context.Context, name string, channel int) {
	key, err := attributevalue.Marshal(name)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}
	id, err := attributevalue.Marshal(channel)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}

	input := &dynamodb.DeleteItemInput{
		TableName: aws.String(ourConfig.TableName),
		Key: map[string]types.AttributeValue{
			"name":      key,
			"channelid": id,
		},
		ReturnValues: types.ReturnValue(*aws.String("ALL_OLD")),
	}

	_, err = tokens.DeleteItem(ctx, input)
	if err != nil {
		log.Printf("failed to delete stale token %s", err)
		return
	}
	tokenCache.Delete(fmt.Sprintf("%s:%d", name, channel))
}

func getToken(ctx context.Context, name string, channel int) string {
	v, ok := tokenCache.Get(fmt.Sprintf("%s:%d", name, channel))
	if ok {
		token, ok := v.(string)
		if ok {
			log.Printf("using cached value for %s %d", name, channel)
			return token
		}
		log.Printf("couldn't cast value?")
	}
	log.Printf("not cached")
	key, err := attributevalue.Marshal(name)
	if err != nil {
		log.Printf("failure to get token for user %s", name)
		return ""
	}
	id, err := attributevalue.Marshal(channel)
	if err != nil {
		log.Printf("failure to get token for user %s", name)
		return ""
	}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(ourConfig.TableName),
		Key: map[string]types.AttributeValue{
			"name":      key,
			"channelid": id,
		},
	}

	log.Printf("Calling Dynamodb with input: %v", input)
	result, err := tokens.GetItem(ctx, input)
	if err != nil {
		log.Printf("failure to get token for user %s %#v %s", name, input, err)
		return ""
	}

	if result.Item == nil {
		log.Printf("failure to get token for user %s %#v", name, result)
		return ""
	}

	t := new(TokenDB)
	err = attributevalue.UnmarshalMap(result.Item, t)
	if err != nil {
		return ""
	}
	tokenCache.Set(fmt.Sprintf("%s:%d", name, channel), t.Token)

	return t.Token
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
			deleteToken(r.Context(), name, int(idN))
			return HandleLogin(w, r)
		}
		m := CreateClipResponse{}
		b, _ := io.ReadAll(resp.Body)
		err = json.Unmarshal(b, &m)
		if err != nil {
			log.Print("no request")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"error": "internal server error unmarshal and read%s"}`, err)))
			w.WriteHeader(http.StatusInternalServerError)
			return nil
		}
		if resp.StatusCode >= http.StatusMultiStatus {
			m2 := TwitchAPIError{}
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
		// displayName=weberr13&provider=twitch
		for _, v := range vals {
			switch {
			case strings.HasPrefix(v, "providerId="):
				id = strings.TrimPrefix(v, "providerId=")
			case strings.HasPrefix(v, "name="):
				name = strings.TrimPrefix(v, "name=")
			}
		}
		cmd = r.URL.Query().Get("cmd")
		idN, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			idN = 0
		}
		token := getToken(r.Context(), name, int(idN))
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
				err := putToken(r.Context(), name, token.AccessToken, int(channelID))
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
