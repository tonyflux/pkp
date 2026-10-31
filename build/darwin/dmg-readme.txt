PKP for PikPak — Read Before First Launch

PKP is not notarized by Apple, so macOS may block the first launch. This is expected. Approve it once using the steps below; each new version may require approval again.

Installation
Drag PKP to the Applications folder, then open it from Applications. Do not run it directly from this disk image.

macOS 15 or later
1. Double-click PKP. When macOS says that PKP was not opened, click Done.
2. Open System Settings → Privacy & Security, scroll to Security, click Open Anyway beside PKP, and authenticate.
3. Open PKP again and click Open Anyway in the confirmation dialog.

macOS 12–14
Control-click PKP in Applications (or right-click it), choose Open, then confirm Open in the dialog.

If those steps fail or macOS says the app is damaged
First verify the download source and checksums. Then open Terminal and run this command for the installed app:
xattr -dr com.apple.quarantine /Applications/PKP.app

System requirements: macOS 12 or later on Apple silicon or Intel.
