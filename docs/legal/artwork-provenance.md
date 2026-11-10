# Artwork provenance / 美术素材来源

This records evidence available for the v0.2.0 source preparation. It is not an independent copyright or trademark clearance. 本记录对应 v0.2.0 源码准备，不等于独立版权或商标授权审计。

## Default wallpapers / 默认壁纸

The design package retains image-generation prompts for the snowy-fjord player concept, including its refined version. Implementation history records extraction of the wallpaper from that concept and the later removal of its blurred playback-control strip. The light wallpaper is a morning-color treatment of the same scene. This supports AI-concept origin rather than a licensed photograph; no particular model identity or generation-service rights are inferred from appearance.

设计包保留了雪山峡湾播放器概念图及精修版的图像生成提示词。实施历史记录从该概念图取出壁纸，再裁掉底部播放控件的模糊区域；浅色版为同一场景的晨雾调色。因此有 AI 概念图来源的记录，不应把它描述成获授权的实拍照片；不根据外观推断具体模型或生成服务的权利条款。

| File / 文件 | SHA-256 |
| --- | --- |
| `frontend/src/assets/wall-dark.webp` | `d14d8bf19b0b83145be2af2ed0c8a5a5ee93ec0829f27e8d51c370fe3188a4a6` |
| `frontend/src/assets/wall-light.webp` | `e79b46b45e7ce656275afa8f3293baf2818ea72f68cb827c9632209bfd787834` |

Local evidence / 本地核对证据：

- Design concept `04-player-liquid-refined.png`: SHA-256 `ac6ca31f9b79d9a9de98cd029fdfeca19f171c010e5cdfd13d4105894828d4e2`; design package records 1536×1024 generated static UI concepts and a snowy-fjord refinement prompt.
- Implementation record dated 2026-10-17: crop box `(3, 6, 1533, 768)`, WebP quality 88, method 6, from the existing extracted wallpaper. Current output is 1530×762. The design website's two same-name wallpapers are byte-identical to the distributed files.
- The wallpapers contain no EXIF attribution or model metadata. The prompt/design/implementation records, not image metadata, establish the recorded chain. A raw original generation result-to-final-pixel derivation has not been independently reproduced.

## Application mark / 应用标志

The design handoff explicitly records a user-provided PikPak mark, an AI material exploration, and a redrawn SVG as the intended final design. The exploration PNG is not the implementation asset. Current `Logo.vue` also identifies the mark as the PikPak box. Do not describe the shipped mark as an independently invented AI logo or imply PikPak endorsement.

设计交接明确记录用户提供的 PikPak 图案、AI 材质探索和作为定稿的重绘 SVG；探索 PNG 不是实施素材。当前 `Logo.vue` 也将图案标为 PikPak box。不能把发行标志描述成独立原创的 AI 商标，也不能暗示 PikPak 背书。

The maintainer has confirmed permission to publish and distribute the current application mark with the open-source project and its installation packages. It is no longer treated as an unresolved publication blocker. This records the maintainer's confirmation, not independently obtained legal clearance or PikPak endorsement; the factual export-chain limits below remain unchanged.

维护者已确认当前应用标志可随开源项目和安装包公开及分发，不再将其列为待确认的发布阻断。此处记录维护者的确认，不声称取得独立法律审计或 PikPak 背书；下述导出链的事实边界保持不变。

Current `build/appicon.png` and `build/appicon.icon/Assets/appicon.png` are byte-identical (SHA-256 `10345bdf11903fea2756dd618eda130de2e5456eddd60ff363167f59877c31d6`). They do not have the same bytes or decoded RGBA pixels as the design handoff's 1024 PNG; a complete export chain to the platform icon containers has not been proven. Trademark/underlying-art permission remains separate from the maintainer's confirmation about authentication identity parameters.

当前两个打包输入一致，但与设计交接的 1024 PNG 的字节及解码像素均不完全一致；尚未证明从设计源到全部平台图标容器的完整导出链。商标和原图权限与维护者对认证参数的确认是不同事项。

## Other packaged and public imagery / 其他发行及公开图像

- DMG background: drawn by tracked `build/darwin/dmg-background.swift` using AppKit gradients, text and arrows; its editable generating source is included.
- TMDB mark: `frontend/src/assets/tmdb.svg` is an external service mark, not claimed as PKP-owned artwork. TMDB attribution remains in THIRD_PARTY.
- Public screenshots: `assets/screenshots/*.png` are isolated running-app captures with synthetic data, Simplified Chinese UI, as described in the Chinese README. The existing English website illustrations listed below were copied from https://pkpapp.cc/en on 2026-11-02 (the site labels them as redrawn from the UI with fictional demo data); they are distinct from captures of a running build. The new `workspace-0.4.0.webp` files in `assets/screenshots/` and `assets/screenshots/en/` capture the current UI in an isolated browser preview with synthetic account, folders, storage figures and bundled wallpaper previews; they do not show production cloud data. This record does not independently clear every embedded image or establish the rights of old binary releases.

  - `assets/screenshots/workspace-0.4.0.webp`: SHA-256 `07a5c2500d67af191cd283cdaa0474075ae3621b764199fc182791dcc6970e70`.
  - `assets/screenshots/en/workspace-0.4.0.webp`: SHA-256 `e9f2896b6efc68c3cc69a1daeaf92b6b85cd80f79673849480f7be0e61519a94`.

| File | Source on pkpapp.cc | SHA-256 |
| --- | --- | --- |
| `assets/screenshots/en/workspace.webp` | `/assets/workspace.BRuflHCi_2jHTsv.webp` | `9579f3aab7d6156f2cd1757435af68ac945dbe5e9dec2ec7db02f09b6329e285` |
| `assets/screenshots/en/assistant.webp` | `/assets/assistant-plan.L1WVrPrf_Z1gkOUP.webp` | `ea8b315b4e7940baecb9edd32dbfd6491bbb93d44280e56ab1fdc14a7f603b0d` |
| `assets/screenshots/en/sync.webp` | `/assets/sync.DuIuEOtF_1asHHf.webp` | `7add6be5a9c369b39d765404d8dc63d6f6b32bd901039bfaeca05cd34422cfc7` |
| `assets/screenshots/en/player.webp` | `/assets/player.DjJ17PWn_ZvyifG.webp` | `8fd06ac99b43a7f3f3eeae371b10c04ddd9ffa5ac6c0eea72d8be9cbd4c5140d` |
| `assets/screenshots/en/accounts.webp` | `/assets/accounts-switch.DBxcG0vA_Z18HVO5.webp` | `4f5c9e087b9933c22bcc35e1bcceada74d2aedf7109f4be8c67cb4782f0878b0` |
| `assets/screenshots/en/app-lock.webp` | `/assets/app-lock.CQg9aKdJ_1xQwuB.webp` | `f6e6861d0558b7dccceb029847374ded32e1d3787c9ca1103af5838c0fbc7239` |

The maintainer's mark-permission confirmation and the incomplete export-chain evidence are distinct. AI use elsewhere does not establish rights or reproduce an export chain. 维护者对标志权限的确认与不完整的导出链证据是不同事项，其他素材使用 AI 不能替代权利确认或复现导出过程。
