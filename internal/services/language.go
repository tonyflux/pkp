package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tonyflux/pkp/internal/i18n"
)

// EventLanguageState carries LanguageState to every window when Interface Language
// changes, so the main window, the player window and the menu bar panel
// switch together.
const EventLanguageState = "language:state"

// LanguageState is Interface Language: the choice, "system" or one of i18n.Langs, and
// the language in force, the system's resolved when following it.
type LanguageState struct {
	Preference string `json:"preference"`
	Locale     string `json:"locale"`
}

func languageState(saved string) LanguageState {
	if l, ok := i18n.Supported(saved); ok {
		return LanguageState{Preference: saved, Locale: string(l)}
	}
	return LanguageState{Preference: "system", Locale: string(i18n.System())}
}

// Language reads Interface Language as saved; a file that names none, or a language
// this build does not have, follows the system.
func (d *DriveService) Language() LanguageState {
	return languageState(d.LocalSettings().Language)
}

// SetLanguage saves Interface Language ("system" or a language), puts it in force for
// Go's own text and tells every window.
func (d *DriveService) SetLanguage(preference string) (LanguageState, error) {
	saved := preference
	if preference == "system" {
		saved = ""
	} else if _, ok := i18n.Supported(preference); !ok {
		return LanguageState{}, i18n.Errorf("不支持的界面语言：%s", preference)
	}
	if err := d.updateSettings(func(s *LocalSettings) { s.Language = saved }); err != nil {
		return LanguageState{}, err
	}
	state := d.ApplyLanguage()
	if app := application.Get(); app != nil {
		app.Event.Emit(EventLanguageState, state)
	}
	return state, nil
}

// ApplyLanguage puts the saved Interface Language in force: Go's text now, the
// system's own dialogs in PKP from the next launch. main calls it before any
// window opens.
//
//wails:ignore
func (d *DriveService) ApplyLanguage() LanguageState {
	state := d.Language()
	i18n.Set(i18n.Lang(state.Locale))
	if state.Preference == "system" {
		i18n.SetAppLanguage("")
	} else {
		i18n.SetAppLanguage(i18n.Lang(state.Locale))
	}
	return state
}
