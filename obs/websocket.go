package obs

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/sceneitems"
	"github.com/andreykaipov/goobs/api/requests/scenes"
	"github.com/andreykaipov/goobs/api/typedefs"
)

// Client is an obs websocket client
type Client struct {
	c *goobs.Client
}

// NewClient for OBS websocket host:  password: "goodpassword"
func NewClient(passwd string) (*Client, error) {
	client, err := goobs.New("localhost:4455", goobs.WithPassword(passwd))
	if err != nil {
		return nil, err
	}
	return &Client{
		c: client,
	}, nil
}

// Close the underlying connection to OBS
func (c *Client) Close() error {
	if c.c != nil {
		err := c.c.Disconnect()
		c.c = nil
		return err
	}
	return nil
}

// GetVersion via websocket
func (c *Client) GetVersion() (string, error) {
	version, err := c.c.General.GetVersion()
	if err != nil {
		return "", err
	}
	s := ""
	s += fmt.Sprintf("OBS Studio version: %s\n", version.ObsVersion)
	s += fmt.Sprintf("Websocket server version: %s\n", version.ObsWebSocketVersion)
	return s, nil
}

// GetScenes that are avaliable
func (c *Client) GetScenes() (string, error) {
	resp, err := c.c.Scenes.GetSceneList()
	if err != nil {
		return "", err
	}
	s := ""
	for _, v := range resp.Scenes {
		s += fmt.Sprintf("%2d %s\n", v.SceneIndex, v.SceneName)
	}
	return s, nil
}

// GetSourcesForCurrentScene returns a list of the scene items for the current scene
func (c *Client) GetSourcesForCurrentScene() (string, []*typedefs.SceneItem, error) {
	resp, err := c.c.Scenes.GetCurrentProgramScene(&scenes.GetCurrentProgramSceneParams{})
	if err != nil {
		return "", nil, err
	}

	resp2, err := c.c.SceneItems.GetSceneItemList(&sceneitems.GetSceneItemListParams{SceneName: resp.CurrentProgramSceneName})
	if err != nil {
		return "", nil, err
	}

	return resp.CurrentProgramSceneName, resp2.SceneItems, nil
}

// SetPromoYoutube sets the promo video
func (c *Client) SetPromoYoutube(promoSourceName string, videoHash string) error {
	// TODO: Extract hash programaticaly
	if strings.HasPrefix(videoHash, "http://") {
		return fmt.Errorf("invalid video link")
	}
	if strings.HasPrefix(videoHash, "https://") {
		if strings.HasPrefix(videoHash, "https://youtu.be") || strings.HasPrefix(videoHash, "https://youtube.com") {
			log.Printf("parsing youtube url")
			u, err := url.Parse(videoHash)
			if err == nil {
				log.Printf("url is %s", u)
				pathElements := strings.Split(u.Path, "/")
				videoHash = pathElements[len(pathElements)-1]
			}
		} else {
			return fmt.Errorf("invalid video link")
		}
	}

	_, sources, err := c.GetSourcesForCurrentScene()
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.SourceName == promoSourceName {
			in, err := c.c.Inputs.GetInputSettings(&inputs.GetInputSettingsParams{
				InputName: source.SourceName,
			})
			if err != nil {
				return err
			}
			log.Printf("input settings %v", *in)
			in.InputSettings["url"] = fmt.Sprintf("https://youtube.com/embed/%s?autoplay=1&modestbranding=1", videoHash)
			_, err = c.c.Inputs.SetInputSettings(&inputs.SetInputSettingsParams{
				InputSettings: in.InputSettings,
				InputName:     source.SourceName,
			})
			return err
			// input settings {browser_source map[reroute_audio:true restart_when_active:true shutdown:true url:https://youtube.com/embed/1Wn6yjwifm4?autoplay=1&modestbranding=1]}
		}
	}
	return nil
}

// ToggleSourceAudio will multe/unmute the named audio source
func (c *Client) ToggleSourceAudio(name string) error {
	_, sources, err := c.GetSourcesForCurrentScene()
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.SourceName == name {
			_, err := c.c.Inputs.ToggleInputMute(&inputs.ToggleInputMuteParams{
				InputName: source.SourceName,
			})
			return err
		}
	}
	return fmt.Errorf("could not find source %s", name)
}

// TogglePromo enables the promo scene if found
func (c *Client) TogglePromo(promoSourceName string) error {
	currentScene, sources, err := c.GetSourcesForCurrentScene()
	if err != nil {
		return err
	}
	newState := false
	for _, source := range sources {
		if source.SourceName == promoSourceName {
			newState = !source.SceneItemEnabled
			_, err = c.c.SceneItems.SetSceneItemEnabled(&sceneitems.SetSceneItemEnabledParams{
				SceneItemEnabled: &newState,
				SceneItemId:      float64(source.SceneItemID),
				SceneName:        currentScene,
			})
			if err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("could not find Promo scene: %s", promoSourceName)
}
