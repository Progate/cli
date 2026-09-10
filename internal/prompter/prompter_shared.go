package prompter

// The pieces every build of gh shares: the interface commands are written
// against, and the search loop that is expressed in terms of it. The
// implementations live next to this file, chosen by build tag - a terminal one
// for normal platforms, a line-based one for wasip1, where there is no terminal
// library that runs in the browser.

import (
	"fmt"
	"slices"
)

//go:generate moq -rm -out prompter_mock.go . Prompter
type Prompter interface {
	// generic prompts from go-gh

	// Select prompts the user to select an option from a list of options.
	Select(prompt string, defaultValue string, options []string) (int, error)
	// MultiSelect prompts the user to select one or more options from a list of options.
	MultiSelect(prompt string, defaults []string, options []string) ([]int, error)
	// MultiSelectWithSearch is MultiSelect with an added search option to the list,
	// prompting the user for text input to filter the options via the searchFunc.
	// Items selected in the search are persisted in the list after subsequent searches.
	// Items passed in persistentOptions are always shown in the list, even when not selected.
	// Unlike MultiSelect, MultiselectWithSearch returns the selected option strings,
	// not their indices, since the list of options is dynamic.
	// The searchFunc has the signature: func(query string) MultiSelectSearchResult.
	// In the returned MultiSelectSearchResult, Keys are the values eventually returned by MultiSelectWithSearch and Labels are what is shown to the user in the prompt.
	MultiSelectWithSearch(prompt, searchPrompt string, defaults []string, persistentOptions []string, searchFunc func(string) MultiSelectSearchResult) ([]string, error)
	// Input prompts the user to enter a string value.
	Input(prompt string, defaultValue string) (string, error)
	// Password prompts the user to enter a password.
	Password(prompt string) (string, error)
	// Confirm prompts the user to confirm an action.
	Confirm(prompt string, defaultValue bool) (bool, error)

	// gh specific prompts

	// AuthToken prompts the user to enter an authentication token.
	AuthToken() (string, error)
	// ConfirmDeletion prompts the user to confirm deletion of a resource by
	// typing the requiredValue.
	ConfirmDeletion(requiredValue string) error
	// InputHostname prompts the user to enter a hostname.
	InputHostname() (string, error)
	// MarkdownEditor prompts the user to edit a markdown document in an editor.
	// If blankAllowed is true, the user can skip the editor and an empty string
	// will be returned.
	MarkdownEditor(prompt string, defaultValue string, blankAllowed bool) (string, error)
}

type MultiSelectSearchResult struct {
	Keys        []string
	Labels      []string
	MoreResults int
	Err         error
}

func multiSelectWithSearch(p Prompter, prompt, searchPrompt string, defaultValues, persistentValues []string, searchFunc func(string) MultiSelectSearchResult) ([]string, error) {
	selectedOptions := defaultValues

	// The optionKeyLabels map is used to uniquely identify optionKeyLabels
	// and provide optional display labels.
	optionKeyLabels := make(map[string]string)
	for _, k := range selectedOptions {
		optionKeyLabels[k] = k
	}

	searchResult := searchFunc("")
	if searchResult.Err != nil {
		return nil, fmt.Errorf("failed to search: %w", searchResult.Err)
	}
	searchResultKeys := searchResult.Keys
	searchResultLabels := searchResult.Labels
	moreResults := searchResult.MoreResults

	for i, k := range searchResultKeys {
		optionKeyLabels[k] = searchResultLabels[i]
	}

	for {
		// Build dynamic option list ->  search sentinel, selections, search results, persistent options.
		optionKeys := make([]string, 0, 1+len(selectedOptions)+len(searchResultKeys)+len(persistentValues))
		optionLabels := make([]string, 0, len(optionKeys))

		// 1. Search sentinel.
		optionKeys = append(optionKeys, "")
		if moreResults > 0 {
			optionLabels = append(optionLabels, fmt.Sprintf("Search (%d more)", moreResults))
		} else {
			optionLabels = append(optionLabels, "Search")
		}

		// 2. Selections
		for _, k := range selectedOptions {
			l := optionKeyLabels[k]

			if l == "" {
				l = k
			}

			optionKeys = append(optionKeys, k)
			optionLabels = append(optionLabels, l)
		}

		// 3. Search results
		for _, k := range searchResultKeys {
			// It's already selected or persistent, if we add here we'll have duplicates.
			if slices.Contains(selectedOptions, k) || slices.Contains(persistentValues, k) {
				continue
			}

			l := optionKeyLabels[k]
			if l == "" {
				l = k
			}
			optionKeys = append(optionKeys, k)
			optionLabels = append(optionLabels, l)
		}

		// 4. Persistent options
		for _, k := range persistentValues {
			if slices.Contains(selectedOptions, k) {
				continue
			}

			l := optionKeyLabels[k]
			if l == "" {
				l = k
			}

			optionKeys = append(optionKeys, k)
			optionLabels = append(optionLabels, l)
		}

		selectedOptionLabels := make([]string, len(selectedOptions))
		for i, k := range selectedOptions {
			l := optionKeyLabels[k]
			if l == "" {
				l = k
			}
			selectedOptionLabels[i] = l
		}

		selectedIdxs, err := p.MultiSelect(prompt, selectedOptionLabels, optionLabels)
		if err != nil {
			return nil, err
		}

		pickedSearch := false
		var newSelectedOptions []string
		for _, idx := range selectedIdxs {
			if idx == 0 { // Search sentinel selected
				pickedSearch = true
				continue
			}

			if idx < 0 || idx >= len(optionKeys) {
				continue
			}

			key := optionKeys[idx]
			if key == "" {
				continue
			}

			newSelectedOptions = append(newSelectedOptions, key)
		}

		selectedOptions = newSelectedOptions
		for _, k := range selectedOptions {
			if _, ok := optionKeyLabels[k]; !ok {
				optionKeyLabels[k] = k
			}
		}

		if pickedSearch {
			query, err := p.Input(searchPrompt, "")
			if err != nil {
				return nil, err
			}

			searchResult := searchFunc(query)
			if searchResult.Err != nil {
				return nil, searchResult.Err
			}
			searchResultKeys = searchResult.Keys
			searchResultLabels = searchResult.Labels
			moreResults = searchResult.MoreResults

			for i, k := range searchResultKeys {
				optionKeyLabels[k] = searchResultLabels[i]
			}

			continue
		}

		return selectedOptions, nil
	}
}
