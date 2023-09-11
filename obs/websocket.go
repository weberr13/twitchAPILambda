package obs

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"github.com/andreykaipov/goobs/api/requests/record"
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

// GetInputs gets all the inputs
func (c *Client) GetInputs() ([]*typedefs.Input, error) {
	resp3, err := c.c.Inputs.GetInputList(&inputs.GetInputListParams{})
	if err == nil {
		// for _, input := range resp3.Inputs {
		// 	log.Printf("input: %s is %v", input.InputName, input.InputKind)
		// }
		return resp3.Inputs, nil
	}
	return nil, err
}

// SetPromoTwitch sets a twitch clip as the next promo vid
func (c *Client) SetPromoTwitch(promoSourceName string, videoURL string) error {
	// https://clips.twitch.tv/embed?clip=ConsiderateTastyTofuUnSane-5fB2PMSUz8PLjrJS
	if !strings.HasPrefix(videoURL, "https://clips.twitch.tv/") {
		return fmt.Errorf("invalid clip link")
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
			in.InputSettings["url"] = videoURL
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

// SetPromoYoutube sets the promo video
func (c *Client) SetPromoYoutube(promoSourceName string, videoHash string) error {
	// TODO: Extract hash programaticaly
	if strings.HasPrefix(videoHash, "http://") {
		return fmt.Errorf("invalid video link")
	}
	if strings.HasPrefix(videoHash, "https://") {
		if strings.HasPrefix(videoHash, "https://youtu.be") ||
			strings.HasPrefix(videoHash, "https://youtube.com") ||
			strings.HasPrefix(videoHash, "https://www.youtube.com") {
			log.Printf("parsing youtube url")
			u, err := url.Parse(videoHash)
			if err == nil {
				qp := u.Query()
				if vParam := qp.Get("v"); vParam != "" {
					videoHash = vParam
				} else {
					pathElements := strings.Split(u.Path, "/")
					videoHash = pathElements[len(pathElements)-1]
				}
				log.Printf("url is %s hash is %s", u, videoHash)

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

// SetSourceVolume in a value 0-100
func (c *Client) SetSourceVolume(name string, val float64) error {
	_, sources, err := c.GetSourcesForCurrentScene()
	if err != nil {
		return err
	}
	if val >= 0 {
		val = -0.01
	}
	if val < -60 {
		val = -60
	}
	for _, source := range sources {
		if source.SourceName == name {
			_, err := c.c.Inputs.SetInputVolume(&inputs.SetInputVolumeParams{
				InputName:     source.SourceName,
				InputVolumeDb: val,
			})
			return err
		}
	}
	return fmt.Errorf("could not find source %s", name)
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
	return fmt.Errorf("could not find source %s in %#v", name, sources)
}

// GetActiveScene gets the name of the active scene
func (c *Client) GetActiveScene() (string, error) {
	resp, err := c.c.Scenes.GetSceneList()
	if err != nil {
		return "", err
	}
	return resp.CurrentProgramSceneName, nil
}

// PauseRecording will pause the recording if running
func (c *Client) PauseRecording() error {
	resp, err := c.c.Record.PauseRecord(&record.PauseRecordParams{})
	if err != nil {
		return err
	}
	log.Printf("pause returned %v", resp)
	return nil
}

// ResumeRecording will resume paused recording
func (c *Client) ResumeRecording() error {
	resp, err := c.c.Record.ResumeRecord(&record.ResumeRecordParams{})
	if err != nil {
		return err
	}
	log.Printf("resume returned %v", resp)
	return nil
}

// SetActiveScene sets the active scene to be the given
func (c *Client) SetActiveScene(name string) error {
	resp, err := c.c.Scenes.SetCurrentProgramScene(&scenes.SetCurrentProgramSceneParams{
		SceneName: name,
	})
	log.Printf("change scene: %#v", resp)
	return err
}

// ToggleInputVolume toggles the mute on an input
func (c *Client) ToggleInputVolume(name string) error {
	ins, err := c.GetInputs()
	if err != nil {
		return err
	}
	for _, input := range ins {
		if input.InputName == name {
			_, err := c.c.Inputs.ToggleInputMute(&inputs.ToggleInputMuteParams{
				InputName: input.InputName,
			})
			return err
		}
	}
	return fmt.Errorf("could not find input: %s in %#v", name, ins)
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
			return nil
		}
	}
	return fmt.Errorf("could not find Promo scene: %s", promoSourceName)
}
