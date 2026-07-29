package config

import "strings"

// ModelSelection is the canonical internal representation for a provider/model choice.
type ModelSelection struct {
	ProviderID       string
	ModelID          string
	ExplicitProvider bool
	Source           string
}

type ResolvedModelSelection struct {
	Requested ModelSelection
	Resolved  ModelSelection
	Reason    string
}

func NewModelSelection(providerID, modelID, source string, explicitProvider bool) ModelSelection {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		explicitProvider = false
	}
	return ModelSelection{
		ProviderID:       providerID,
		ModelID:          modelID,
		ExplicitProvider: explicitProvider,
		Source:           strings.TrimSpace(source),
	}
}

func ParseModelSelection(selection string, source string) ModelSelection {
	providerID, modelID := ParseModel(strings.TrimSpace(selection))
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	explicitProvider := providerID != ""
	if modelID == "" && providerID != "" {
		modelID = providerID
		providerID = ""
		explicitProvider = false
	}
	return NewModelSelection(providerID, modelID, source, explicitProvider)
}

func (selection ModelSelection) Ref() string {
	providerID := strings.TrimSpace(selection.ProviderID)
	modelID := strings.TrimSpace(selection.ModelID)
	if modelID == "" {
		return providerID
	}
	if providerID == "" {
		return modelID
	}
	return providerID + "/" + modelID
}
