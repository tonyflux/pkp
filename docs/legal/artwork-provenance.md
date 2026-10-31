# Artwork provenance / 美术素材来源

This records evidence available for the v0.3.0 source preparation. It is not an independent copyright or trademark clearance. 本记录对应 v0.3.0 源码准备，不等于独立版权或商标授权审计。

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

Current `build/appicon.png` and `build/appicon.icon/Assets/appicon.png` are byte-identical (SHA-256 `10345bdf11903fea2756dd618eda130de2e5456eddd60ff363167f59877c31d6`). They do not have the same bytes or decoded RGBA pixels as the design handoff's 1024 PNG; a complete export chain to the platform icon containers has not been proven. Trademark/underlying-art permission remains separate from the maintainer's confirmation about authentication identity parameters.

当前两个打包输入一致，但与设计交接的 1024 PNG 的字节及解码像素均不完全一致；尚未证明从设计源到全部平台图标容器的完整导出链。商标和原图权限与维护者对认证参数的确认是不同事项。

## Other packaged and public imagery / 其他发行及公开图像

- DMG background: drawn by tracked `build/darwin/dmg-background.swift` using AppKit gradients, text and arrows; its editable generating source is included.
- TMDB mark: `frontend/src/assets/tmdb.svg` is an external service mark, not claimed as PKP-owned artwork. TMDB attribution remains in THIRD_PARTY.
- Public screenshots: use isolated UI and synthetic data as described in README. This does not independently clear every embedded image or establish the rights of old binary releases.

The remaining mark permissions and incomplete export chains must not be presented as resolved merely because AI was used elsewhere. 不能因为其他素材使用了 AI，就宣称标志授权及不完整导出链已解决。
