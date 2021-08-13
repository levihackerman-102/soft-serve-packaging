package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"smoothie/git"
	"smoothie/tui/bubbles/commits"
	"smoothie/tui/bubbles/selection"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gliderlabs/ssh"
)

type sessionState int

const (
	startState sessionState = iota
	errorState
	loadedState
	quittingState
	quitState
)

type Config struct {
	Name         string      `json:"name"`
	Host         string      `json:"host"`
	Port         int64       `json:"port"`
	ShowAllRepos bool        `json:"show_all_repos"`
	Menu         []MenuEntry `json:"menu"`
	RepoSource   *git.RepoSource
}

type MenuEntry struct {
	Name string `json:"name"`
	Note string `json:"note"`
	Repo string `json:"repo"`
}

type SessionConfig struct {
	Width         int
	Height        int
	WindowChanges <-chan ssh.Window
}

type Bubble struct {
	config        *Config
	state         sessionState
	error         string
	width         int
	height        int
	windowChanges <-chan ssh.Window
	repoSource    *git.RepoSource
	repoMenu      []MenuEntry
	repos         []*git.Repo
	boxes         []tea.Model
	activeBox     int
	repoSelect    *selection.Bubble
	commitsLog    *commits.Bubble
}

func NewBubble(cfg *Config, sCfg *SessionConfig) *Bubble {
	b := &Bubble{
		config:        cfg,
		width:         sCfg.Width,
		height:        sCfg.Height,
		windowChanges: sCfg.WindowChanges,
		repoSource:    cfg.RepoSource,
		boxes:         make([]tea.Model, 2),
	}
	b.state = startState
	return b
}

func (b *Bubble) Init() tea.Cmd {
	return tea.Batch(b.windowChangesCmd, b.setupCmd)
}

func (b *Bubble) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0)
	// Always allow state, error, info, window resize and quit messages
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return b, tea.Quit
		case "tab":
			b.activeBox = (b.activeBox + 1) % 2
		}
	case errMsg:
		b.error = msg.Error()
		b.state = errorState
		return b, nil
	case windowMsg:
		cmds = append(cmds, b.windowChangesCmd)
	case tea.WindowSizeMsg:
		b.width = msg.Width
		b.height = msg.Height
	case selection.SelectedMsg:
		b.activeBox = 1
		cmds = append(cmds, b.getRepoCmd(b.repoMenu[msg.Index].Repo))
	case selection.ActiveMsg:
		cmds = append(cmds, b.getRepoCmd(b.repoMenu[msg.Index].Repo))
	}
	if b.state == loadedState {
		ab, cmd := b.boxes[b.activeBox].Update(msg)
		b.boxes[b.activeBox] = ab
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return b, tea.Batch(cmds...)
}

func (b *Bubble) viewForBox(i int, width int) string {
	var ls lipgloss.Style
	if i == b.activeBox {
		ls = activeBoxStyle.Width(width)
	} else {
		ls = inactiveBoxStyle.Width(width)
	}
	return ls.Render(b.boxes[i].View())
}

func (b *Bubble) View() string {
	h := headerStyle.Width(b.width - horizontalPadding).Render(b.config.Name)
	f := footerStyle.Render("")
	s := ""
	content := ""
	switch b.state {
	case loadedState:
		lb := b.viewForBox(0, boxLeftWidth)
		rb := b.viewForBox(1, boxRightWidth)
		s += lipgloss.JoinHorizontal(lipgloss.Top, lb, rb)
	case errorState:
		s += errorStyle.Render(fmt.Sprintf("Bummer: %s", b.error))
	default:
		s = normalStyle.Render(fmt.Sprintf("Doing something weird %d", b.state))
	}
	content = h + "\n\n" + s + "\n" + f
	return appBoxStyle.Render(content)
}

func loadConfig(rs *git.RepoSource) (*Config, error) {
	cfg := &Config{}
	cfg.RepoSource = rs
	cr, err := rs.GetRepo("config")
	if err != nil {
		return nil, fmt.Errorf("cannot load config repo: %s", err)
	}
	cs, err := cr.LatestFile("config.json")
	if err != nil {
		return nil, fmt.Errorf("cannot load config.json: %s", err)
	}
	err = json.Unmarshal([]byte(cs), cfg)
	if err != nil {
		return nil, fmt.Errorf("bad json in config.json: %s", err)
	}
	return cfg, nil
}

func SessionHandler(reposPath string, repoPoll time.Duration) func(ssh.Session) (tea.Model, []tea.ProgramOption) {
	rs := git.NewRepoSource(reposPath)
	err := createDefaultConfigRepo(rs)
	if err != nil {
		if err != nil {
			log.Fatalf("cannot create config repo: %s", err)
		}
	}
	appCfg, err := loadConfig(rs)
	if err != nil {
		if err != nil {
			log.Printf("cannot load config: %s", err)
		}
	}
	go func() {
		for {
			time.Sleep(repoPoll)
			err := rs.LoadRepos()
			if err != nil {
				log.Printf("cannot load repos: %s", err)
				continue
			}
			cfg, err := loadConfig(rs)
			if err != nil {
				if err != nil {
					log.Printf("cannot load config: %s", err)
					continue
				}
			}
			appCfg = cfg
		}
	}()

	return func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
		if len(s.Command()) == 0 {
			pty, changes, active := s.Pty()
			if !active {
				return nil, nil
			}
			cfg := &SessionConfig{
				Width:         pty.Window.Width,
				Height:        pty.Window.Height,
				WindowChanges: changes,
			}
			return NewBubble(appCfg, cfg), []tea.ProgramOption{tea.WithAltScreen()}
		}
		return nil, nil
	}
}
