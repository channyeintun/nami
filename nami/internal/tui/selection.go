package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
)

func selectionItems(options []selectionOptionState) []list.Item {
	items := make([]list.Item, 0, len(options))
	for _, option := range options {
		items = append(items, option)
	}
	return items
}

func selectionListTitle(request selectionRequestState) string {
	if request.Title != "" {
		return request.Title
	}
	return fmt.Sprintf("%s selection", request.Kind)
}
