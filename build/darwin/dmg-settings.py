from pathlib import Path

root = Path.cwd()
app = Path(defines.get("app", root / "bin/pkp.app")).resolve()

format = "UDZO"
filesystem = "HFS+"
files = [(str(app), "PKP.app")]
symlinks = {"Applications": "/Applications"}
icon = str(root / "build/darwin/icons.icns")
background = str(root / "bin/dmg-background.tiff")

# Finder may keep global path/status bars: reserve 120pt of window chrome for the 440pt artwork.
window_rect = ((200, 200), (640, 560))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
arrange_by = None
icon_size = 104
text_size = 14
icon_locations = {"PKP.app": (172, 230), "Applications": (468, 230)}
