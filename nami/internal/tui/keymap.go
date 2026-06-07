package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Submit      key.Binding
	Cancel      key.Binding
	Quit        key.Binding
	Help        key.Binding
	Complete    key.Binding
	HistoryPrev key.Binding
	HistoryNext key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "send"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "cancel/quit"),
		),
		Quit: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Complete: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "complete"),
		),
		HistoryPrev: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("up", "history"),
		),
		HistoryNext: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("down", "history"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Submit, k.Complete, k.HistoryPrev, k.Cancel, k.Quit, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Submit, k.Complete, k.HistoryPrev, k.HistoryNext},
		{k.Cancel, k.Quit, k.Help},
	}
}
