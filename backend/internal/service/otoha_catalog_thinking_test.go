//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func otohaBool(v bool) *bool { return &v }

func TestOtohaThinkingOfAModelNobodyDescribedIsUnknown(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "deepseek-v4", Enabled: true})

	body, err := json.Marshal(otohaModelByID(t, f.catalog(t, otohaGroupID), "deepseek-v4"))
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &got))
	require.JSONEq(t, `null`, string(got["thinking"]))
	require.JSONEq(t, `[]`, string(got["levels"]))
}

func TestOtohaThinkingAndLevelsAreStoredAndReported(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "claude-opus", Enabled: true, Complexity: "complex",
		Thinking: &OtohaThinking{CanBeOff: otohaBool(false), Settings: []OtohaThinkingSetting{
			{Value: "Adaptive"}, {Value: "between_tools", MaxEffort: "High"},
		}},
		Levels: []OtohaThinkingLevel{
			{Setting: "between_tools", Effort: "low", Complexity: "simple", ExtraOutput: "low"},
			{Setting: "adaptive", Effort: "high", Complexity: "complex", ExtraOutput: "high"},
		},
	})

	model := otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus")
	body, err := json.Marshal(model)
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &got))
	require.JSONEq(t, `{"canBeOff":false,"settings":[{"value":"adaptive"},{"value":"between_tools","maxEffort":"high"}]}`, string(got["thinking"]))
	require.JSONEq(t, `[{"setting":"between_tools","effort":"low","complexity":"simple","extraOutput":"low"},
		{"setting":"adaptive","effort":"high","complexity":"complex","extraOutput":"high"}]`, string(got["levels"]))
	require.JSONEq(t, `"complex"`, string(got["complexity"]), "the old complexity field stays for older apps")
}

func TestOtohaThinkingWithUnknownCanBeOffIsNull(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{
		ModelID: "claude-opus", Enabled: true,
		Thinking: &OtohaThinking{Settings: []OtohaThinkingSetting{{Value: "budget"}}},
	})
	body, err := json.Marshal(otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus").Thinking)
	require.NoError(t, err)
	require.JSONEq(t, `{"canBeOff":null,"settings":[{"value":"budget"}]}`, string(body))
}

func TestOtohaEmptyThinkingIsUnknown(t *testing.T) {
	f := newOtohaCatalogFixture()
	f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true, Thinking: &OtohaThinking{}})
	require.Nil(t, otohaModelByID(t, f.catalog(t, otohaGroupID), "claude-opus").Thinking)
}

func TestOtohaThinkingInputIsChecked(t *testing.T) {
	f := newOtohaCatalogFixture()
	cases := map[string]OtohaCatalogEntryInput{
		"unknown setting":          {Thinking: &OtohaThinking{Settings: []OtohaThinkingSetting{{Value: "turbo"}}}},
		"duplicate setting":        {Thinking: &OtohaThinking{Settings: []OtohaThinkingSetting{{Value: "adaptive"}, {Value: "adaptive"}}}},
		"bad effort name":          {Thinking: &OtohaThinking{Settings: []OtohaThinkingSetting{{Value: "off", MaxEffort: "Very High!"}}}},
		"cannot be off but has it": {Thinking: &OtohaThinking{CanBeOff: otohaBool(false), Settings: []OtohaThinkingSetting{{Value: "off"}}}},
		"level without setting":    {Levels: []OtohaThinkingLevel{{Complexity: "simple", ExtraOutput: "low"}}},
		"level bad complexity":     {Levels: []OtohaThinkingLevel{{Setting: "adaptive", Complexity: "huge", ExtraOutput: "low"}}},
		"level no complexity":      {Levels: []OtohaThinkingLevel{{Setting: "adaptive", ExtraOutput: "low"}}},
		"level bad extra output":   {Levels: []OtohaThinkingLevel{{Setting: "adaptive", Complexity: "simple", ExtraOutput: "lots"}}},
		"level bad effort":         {Levels: []OtohaThinkingLevel{{Setting: "adaptive", Effort: "A B", Complexity: "simple", ExtraOutput: "low"}}},
		"too many levels":          {Levels: make([]OtohaThinkingLevel, otohaThinkingLevelsMax+1)},
	}
	for name, input := range cases {
		input.ModelID, input.Enabled = "gpt-6-luna", true
		_, err := f.svc.CreateEntry(context.Background(), otohaGroupID, input)
		require.Error(t, err, name)
	}
}

func TestOtohaRevisionChangesWithThinkingAndLevels(t *testing.T) {
	f := newOtohaCatalogFixture()
	entry := f.add(t, otohaGroupID, OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true})
	first := f.catalog(t, otohaGroupID).Revision

	input := OtohaCatalogEntryInput{ModelID: "claude-opus", Enabled: true,
		Thinking: &OtohaThinking{CanBeOff: otohaBool(true), Settings: []OtohaThinkingSetting{{Value: "off", MaxEffort: "high"}}}}
	_, err := f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	second := f.catalog(t, otohaGroupID).Revision
	require.NotEqual(t, first, second)

	input.Levels = []OtohaThinkingLevel{{Setting: "off", Complexity: "simple", ExtraOutput: "low"}}
	_, err = f.svc.UpdateEntry(context.Background(), otohaGroupID, entry.ID, input)
	require.NoError(t, err)
	require.NotEqual(t, second, f.catalog(t, otohaGroupID).Revision)
}
