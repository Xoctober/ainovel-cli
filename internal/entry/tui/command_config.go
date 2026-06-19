package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

type configSwitchState struct {
	items   []host.ConfigProfileOption
	cursor  int
	message string
}

func newConfigSwitchState(rt *host.Host) *configSwitchState {
	state := &configSwitchState{items: rt.ConfigProfiles()}
	if len(state.items) == 0 {
		state.message = "当前没有可用配置，请在 ./configs 下添加 JSON 配置"
		return state
	}
	for i, item := range state.items {
		if item.Active {
			state.cursor = i
			break
		}
	}
	return state
}

func (s *configSwitchState) selected() (host.ConfigProfileOption, bool) {
	if s == nil || s.cursor < 0 || s.cursor >= len(s.items) {
		return host.ConfigProfileOption{}, false
	}
	return s.items[s.cursor], true
}

func (s *configSwitchState) move(delta int) {
	if len(s.items) == 0 {
		return
	}
	total := len(s.items)
	s.cursor = (s.cursor + delta + total) % total
}

func (s *configSwitchState) apply(rt *host.Host) error {
	item, ok := s.selected()
	if !ok {
		return fmt.Errorf("当前没有可用配置")
	}
	return rt.SwitchConfigProfile(item.Path)
}

func (m Model) handleConfigSwitchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.configSwitch == nil {
		return m, nil
	}
	state := m.configSwitch

	switch msg.Type {
	case tea.KeyEsc:
		m.configSwitch = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		state.move(-1)
		return m, nil
	case tea.KeyDown:
		state.move(1)
		return m, nil
	case tea.KeyEnter:
		if err := state.apply(m.runtime); err != nil {
			state.message = err.Error()
			return m, nil
		}
		m.configSwitch = nil
		return m, tea.Batch(m.textarea.Focus(), fetchSnapshot(m.runtime))
	default:
		return m, nil
	}
}

func renderConfigSwitchBar(width int, state *configSwitchState) string {
	if state == nil || width <= 0 {
		return ""
	}

	title := lipgloss.NewStyle().
		Foreground(colorMuted).
		Bold(true).
		Render("/chageConfig 切换配置")

	boxW := width - 2
	if boxW > 76 {
		boxW = 76
	}
	if boxW < 56 {
		boxW = 56
	}
	contentW := boxW - 4
	if contentW < 20 {
		contentW = 20
	}

	var body []string
	if len(state.items) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(colorError).Render(state.message))
	} else {
		start, end := commandPaletteWindow(len(state.items), state.cursor, 6)
		for i, item := range state.items[start:end] {
			idx := start + i
			prefix := "  "
			nameStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
			if idx == state.cursor {
				prefix = "› "
				nameStyle = nameStyle.Foreground(colorAccent).Bold(true).Underline(true)
			}
			filename := filepath.Base(item.Path)
			if filename == "." || filename == "" {
				filename = item.Name
			}
			name := nameStyle.Render(truncateWidth(filename, contentW-lipgloss.Width(prefix)))
			body = append(body, prefix+name)
		}
		hint := lipgloss.NewStyle().
			Foreground(colorDim).
			Italic(true).
			Render("↑↓ 切换配置   Enter 应用   Esc 取消")
		body = append(body, hint)
		if state.message != "" {
			body = append(body, lipgloss.NewStyle().Foreground(colorError).Italic(true).Render(truncate(state.message, contentW)))
		}
	}

	innerW := boxW - 2
	sepW := innerW - lipgloss.Width(title) - 3
	if sepW < 0 {
		sepW = 0
	}
	lineStyle := lipgloss.NewStyle().Foreground(colorDim)
	topBorder := lineStyle.Render("┌─ ") + title + lineStyle.Render(" "+strings.Repeat("─", sepW)+"┐")
	bottomBorder := lineStyle.Render("└" + strings.Repeat("─", innerW) + "┘")

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, topBorder)
	for _, line := range body {
		padding := innerW - lipgloss.Width(line)
		if padding < 0 {
			padding = 0
		}
		lines = append(lines, lineStyle.Render("│")+line+strings.Repeat(" ", padding)+lineStyle.Render("│"))
	}
	lines = append(lines, bottomBorder)
	return strings.Join(lines, "\n")
}
