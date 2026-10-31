package i18n

import "golang.org/x/sys/windows"

func preferred() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}

// SetAppLanguage does nothing here: Windows draws its dialogs in the
// user's display language whatever the app asks.
func SetAppLanguage(Lang) {}
