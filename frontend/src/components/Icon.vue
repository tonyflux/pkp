<script setup lang="ts">
import { computed } from "vue";

// One thin line system at a single stroke weight. No emoji:
// they render differently per platform and carry no accessible name.
const paths: Record<string, string> = {
  folder: "M3 7.5A2.5 2.5 0 0 1 5.5 5h3.2a2 2 0 0 1 1.5.7l1 1.2h7.3A2.5 2.5 0 0 1 21 9.4v7.1A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5z",
  file: "M14 3H7.5A2.5 2.5 0 0 0 5 5.5v13A2.5 2.5 0 0 0 7.5 21h9a2.5 2.5 0 0 0 2.5-2.5V8zM14 3v5h5",
  video: "M4 6.5A2.5 2.5 0 0 1 6.5 4h7A2.5 2.5 0 0 1 16 6.5v11A2.5 2.5 0 0 1 13.5 20h-7A2.5 2.5 0 0 1 4 17.5zM16 10l4-2.5v9L16 14",
  image: "M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v11a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 17.5zM4 16l4.2-4.2a2 2 0 0 1 2.8 0L20 20M14.5 9.5h.01",
  audio: "M9 18V5.5l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 15.5a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z",
  document: "M14 3H7.5A2.5 2.5 0 0 0 5 5.5v13A2.5 2.5 0 0 0 7.5 21h9a2.5 2.5 0 0 0 2.5-2.5V8zM14 3v5h5M8.5 13h7M8.5 16.5h4",
  archive: "M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5v11a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 17.5zM10.5 4v4M13.5 8v4M10.5 12v4M13.5 16v2",
  star: "M12 4.2l2.45 4.96 5.47.8-3.96 3.86.93 5.45L12 16.7l-4.89 2.57.93-5.45L4.08 9.96l5.47-.8z",
  clock: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7.5V12l3 2",
  play: "M8 5.5l11 6.5-11 6.5z",
  // Two rounded bars, drawn as a filled shape so the main button reads as
  // solidly as its play triangle does.
  pause: "M7.75 5.5h2.5a.75.75 0 0 1 .75.75v11.5a.75.75 0 0 1-.75.75h-2.5a.75.75 0 0 1-.75-.75V6.25a.75.75 0 0 1 .75-.75zM13.75 5.5h2.5a.75.75 0 0 1 .75.75v11.5a.75.75 0 0 1-.75.75h-2.5a.75.75 0 0 1-.75-.75V6.25a.75.75 0 0 1 .75-.75z",
  cloudAdd: "M7 18.5a4.5 4.5 0 0 1-.4-8.98 5.5 5.5 0 0 1 10.6-1.1A4.25 4.25 0 0 1 17.5 18.5zM12 10.5v6M9 13.5h6",
  transfer: "M8 4.5v15M8 19.5l-3.2-3.4M8 19.5l3.2-3.4M16 19.5v-15M16 4.5l-3.2 3.4M16 4.5l3.2 3.4",
  share: "M12 15.5V4.2M12 4.2L8.2 8M12 4.2L15.8 8M5 13.5v4.9A2.6 2.6 0 0 0 7.6 21h8.8a2.6 2.6 0 0 0 2.6-2.6v-4.9",
  trash: "M5 7h14M10 7V5.4A1.4 1.4 0 0 1 11.4 4h1.2A1.4 1.4 0 0 1 14 5.4V7M6.5 7l.8 11.2A2 2 0 0 0 9.3 20h5.4a2 2 0 0 0 2-1.8L17.5 7M10.5 11v5.5M13.5 11v5.5",
  restore: "M4.5 9.5h5v-5M4.9 12a7.2 7.2 0 1 0 1.9-5.1L4.5 9",
  search: "M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM16.2 16.2L21 21",
  chevronLeft: "M14.5 5.5L8 12l6.5 6.5",
  chevronRight: "M9.5 5.5L16 12l-6.5 6.5",
  chevronDown: "M5.5 9.5L12 16l6.5-6.5",
  grid: "M4.5 5.5h6v6h-6zM13.5 5.5h6v6h-6zM4.5 14.5h6v6h-6zM13.5 14.5h6v6h-6z",
  list: "M4.5 7h2M9.5 7h10M4.5 12h2M9.5 12h10M4.5 17h2M9.5 17h10",
  sort: "M7 4.5v15M7 19.5l-3-3.2M7 19.5l3-3.2M14 7.5h6M14 12h5M14 16.5h4",
  close: "M6.5 6.5l11 11M17.5 6.5l-11 11",
  more: "M6.5 12h.01M12 12h.01M17.5 12h.01",
  download: "M12 4.5v10.5M12 15l-3.6-3.6M12 15l3.6-3.6M5 17.5v1.1A2.4 2.4 0 0 0 7.4 21h9.2a2.4 2.4 0 0 0 2.4-2.4v-1.1",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v5.5M12 7.6h.01",
  check: "M5.5 12.5l4.2 4.2L18.5 8",
  alert: "M12 8v5M12 16.4h.01M10.3 4.2L3.5 16.4A2 2 0 0 0 5.2 19.5h13.6a2 2 0 0 0 1.7-3.1L13.7 4.2a2 2 0 0 0-3.4 0z",
  plus: "M12 5.5v13M5.5 12h13",
  settings: "M12 15.2a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4zM19.3 14.4l.9 1.5-2 3.4-1.7-.5a7 7 0 0 1-1.6.9l-.4 1.8h-4l-.4-1.8a7 7 0 0 1-1.6-.9l-1.7.5-2-3.4.9-1.5a7 7 0 0 1 0-1.8l-.9-1.5 2-3.4 1.7.5a7 7 0 0 1 1.6-.9L10.5 3h4l.4 1.8a7 7 0 0 1 1.6.9l1.7-.5 2 3.4-.9 1.5a7 7 0 0 1 0 1.8z",
  user: "M12 12.5a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4.5 20.3a7.6 7.6 0 0 1 15 0",
  volume: "M11 5.5L6.8 9H4.2A1.2 1.2 0 0 0 3 10.2v3.6A1.2 1.2 0 0 0 4.2 15h2.6L11 18.5zM15 9.6a3.4 3.4 0 0 1 0 4.8M17.8 7a7 7 0 0 1 0 10",
  mute: "M11 5.5L6.8 9H4.2A1.2 1.2 0 0 0 3 10.2v3.6A1.2 1.2 0 0 0 4.2 15h2.6L11 18.5zM15.5 10l5 4M20.5 10l-5 4",
  // A 270° ring open at the top-right, with the arrowhead at the ring's
  // top end pointing the way it turns. The centre is left clear for the
  // "15" the buttons overlay.
  back15: "M12 5a7.5 7.5 0 1 0 7.5 7.5M15 2.5L12 5l3 2.5",
  forward15: "M12 5a7.5 7.5 0 1 1-7.5 7.5M9 2.5L12 5 9 7.5",
  pip: "M4 7.5A2.5 2.5 0 0 1 6.5 5h11A2.5 2.5 0 0 1 20 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 16.5zM12.5 12.5h5.5v4.5h-5.5z",
  fullscreen: "M4.5 9V5.5a1 1 0 0 1 1-1H9M15 4.5h3.5a1 1 0 0 1 1 1V9M19.5 15v3.5a1 1 0 0 1-1 1H15M9 19.5H5.5a1 1 0 0 1-1-1V15",
  repeat: "M6.5 7.5h10.2a2.8 2.8 0 0 1 2.8 2.8v.7M17.5 16.5H7.3a2.8 2.8 0 0 1-2.8-2.8v-.7M6.5 7.5l2.6-2.6M6.5 7.5l2.6 2.6M17.5 16.5l-2.6 2.6M17.5 16.5l-2.6-2.6",
  link: "M10 13.8a3.6 3.6 0 0 0 5.3.4l2.6-2.6a3.6 3.6 0 0 0-5.1-5.1l-1.5 1.5M14 10.2a3.6 3.6 0 0 0-5.3-.4l-2.6 2.6a3.6 3.6 0 0 0 5.1 5.1l1.5-1.5",
  subtitles: "M4 7.5A2.5 2.5 0 0 1 6.5 5h11A2.5 2.5 0 0 1 20 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-11A2.5 2.5 0 0 1 4 16.5zM7 11h5M14 11h3M7 14.5h3M12 14.5h5",
  waveform: "M4 12v.01M7.5 9v6M11 6v12M14.5 8.5v7M18 10v4M21 12v.01",
  shuffle: "M4 7h2.4a4 4 0 0 1 3.3 1.8l4.6 6.4a4 4 0 0 0 3.3 1.8H20M4 17h2.4a4 4 0 0 0 3.3-1.8l.8-1.1M14.3 8.8l.3-.4A4 4 0 0 1 17.6 7H20M17.5 4.5L20 7l-2.5 2.5M17.5 14.5L20 17l-2.5 2.5",
  repeatOne: "M6.5 7.5h10.2a2.8 2.8 0 0 1 2.8 2.8v.7M17.5 16.5H7.3a2.8 2.8 0 0 1-2.8-2.8v-.7M6.5 7.5l2.6-2.6M6.5 7.5l2.6 2.6M17.5 16.5l-2.6 2.6M17.5 16.5l-2.6-2.6M12 9.5v5M11 10.5l1-1",
  stopAfter: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM9.5 9.5h5v5h-5z",
  gauge: "M4.5 15.5a7.5 7.5 0 1 1 15 0M12 15.5l3.5-5M12 15.5h.01",
  exitFullscreen: "M9 4.5V8a1 1 0 0 1-1 1H4.5M15 4.5V8a1 1 0 0 0 1 1h3.5M19.5 15H16a1 1 0 0 0-1 1v3.5M4.5 15H8a1 1 0 0 1 1 1v3.5",
  eye: "M2.5 12s3.5-6.5 9.5-6.5 9.5 6.5 9.5 6.5-3.5 6.5-9.5 6.5S2.5 12 2.5 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z",
  minus: "M5.5 12h13",
  stop: "M6 8.5A2.5 2.5 0 0 1 8.5 6h7A2.5 2.5 0 0 1 18 8.5v7a2.5 2.5 0 0 1-2.5 2.5h-7A2.5 2.5 0 0 1 6 15.5z",
  filter: "M4.5 7h15M7.5 12h9M10.5 17h3",
  // Shares, reader and connections.
  shares: "M9.5 11.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM3.5 20a6 6 0 0 1 12 0M16 4.8a3.5 3.5 0 0 1 0 6.4M17.5 14.3a6 6 0 0 1 3 5.7",
  lock: "M6.5 10.5h11a1.5 1.5 0 0 1 1.5 1.5v7a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 5 19v-7a1.5 1.5 0 0 1 1.5-1.5zM8 10.5V8a4 4 0 0 1 8 0v2.5M12 14.5v2",
  copy: "M9 9.5A1.5 1.5 0 0 1 10.5 8h8A1.5 1.5 0 0 1 20 9.5v8a1.5 1.5 0 0 1-1.5 1.5h-8A1.5 1.5 0 0 1 9 17.5zM15 8V5.5A1.5 1.5 0 0 0 13.5 4h-8A1.5 1.5 0 0 0 4 5.5v8A1.5 1.5 0 0 0 5.5 15H8",
  globe: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3z",
  key: "M14.5 13.5a4.5 4.5 0 1 0-4.2-2.9L4 16.9V20h3.1v-2.2h2.2v-2.2h2.2l1.5-1.5a4.5 4.5 0 0 0 1.5-.6zM15.5 8.5h.01",
  apps: "M4.5 5.5h5v5h-5zM14.5 5.5h5v5h-5zM4.5 14.5h5v5h-5zM14.5 14.5h5v5h-5z",
  chip: "M8 8h8v8H8zM4 9.5h2M4 14.5h2M18 9.5h2M18 14.5h2M9.5 4v2M14.5 4v2M9.5 18v2M14.5 18v2",
  laptop: "M6.5 5h11A1.5 1.5 0 0 1 19 6.5V15H5V6.5A1.5 1.5 0 0 1 6.5 5zM3 18.5h18",
  eyeOff: "M3.5 3.5l17 17M10.4 5.6a9 9 0 0 1 1.6-.1c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-2.8 3.6M6.7 6.7C4 8.4 2.5 12 2.5 12s3.5 6.5 9.5 6.5a9.4 9.4 0 0 0 4.3-1M9.9 9.9a3 3 0 0 0 4.2 4.2",
  terminal: "M4.5 5h15A1.5 1.5 0 0 1 21 6.5v11a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 17.5v-11A1.5 1.5 0 0 1 4.5 5zM7 9.5l2.5 2.5L7 14.5M12 14.5h4.5",
  keyboard: "M4.5 6h15A1.5 1.5 0 0 1 21 7.5v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 16.5v-9A1.5 1.5 0 0 1 4.5 6zM7 9.5h.01M10 9.5h.01M13 9.5h.01M16.5 9.5h.01M7 12.5h.01M10 12.5h.01M13 12.5h.01M16.5 12.5h.01M8.5 15.2h7",
  font: "M5 18.5L10.2 5.5h1.6L17 18.5M7.2 13h7.6M19 18.5v-6M16.5 15h5",
  palette: "M12 21a9 9 0 1 1 8.6-11.6c.6 2-1 3.6-3 3.6h-1.4a2 2 0 0 0-1.5 3.3c1 1.2.2 3-1.3 3.4a9 9 0 0 1-1.4.3zM8 10.5h.01M11.5 7h.01M15.5 8.5h.01M7.5 14.5h.01",
  text: "M14 3H7.5A2.5 2.5 0 0 0 5 5.5v13A2.5 2.5 0 0 0 7.5 21h9a2.5 2.5 0 0 0 2.5-2.5V8zM14 3v5h5M8.5 12h7M8.5 15.5h7M8.5 9h2",
  calendar: "M4.5 7.5A2 2 0 0 1 6.5 5.5h11a2 2 0 0 1 2 2v11a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2zM4.5 10.5h15M8.5 3.5v4M15.5 3.5v4",
  pin: "M9 4.5h6M10 4.5v5.2L7.3 12.5a1 1 0 0 0 .7 1.7H16a1 1 0 0 0 .7-1.7L14 9.7V4.5M12 14.2V20",
  upload: "M12 15.5V5M12 5L8.4 8.6M12 5l3.6 3.6M5 17.5v1.1A2.4 2.4 0 0 0 7.4 21h9.2a2.4 2.4 0 0 0 2.4-2.4v-1.1",
  chevronUp: "M5.5 14.5L12 8l6.5 6.5",
  expand: "M14 4.5h5.5V10M19.5 4.5L13.5 10.5M10 19.5H4.5V14M4.5 19.5l6-6",
  magnet: "M6 3.5v7.5a6 6 0 0 0 12 0V3.5M10 3.5v7.5a2 2 0 0 0 4 0V3.5M6 7.5h4M14 7.5h4",
  move: "M3 7.5A2.5 2.5 0 0 1 5.5 5h3.2a2 2 0 0 1 1.5.7l1 1.2h7.3A2.5 2.5 0 0 1 21 9.4v7.1A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5zM8.5 13h7M13 10.5l2.5 2.5-2.5 2.5",
  // New: a folder to make, and a folder to send up.
  folderPlus: "M3 7.5A2.5 2.5 0 0 1 5.5 5h3.2a2 2 0 0 1 1.5.7l1 1.2h7.3A2.5 2.5 0 0 1 21 9.4v7.1A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5zM12 10v6M9 13h6",
  folderUpload: "M3 7.5A2.5 2.5 0 0 1 5.5 5h3.2a2 2 0 0 1 1.5.7l1 1.2h7.3A2.5 2.5 0 0 1 21 9.4v7.1A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5zM12 16.2V10M9.5 12.5L12 10l2.5 2.5",
  // The separate player window: out of this window, and back into it.
  popout: "M13.5 4.5h6v6M19.5 4.5L12 12M10.5 5H7a2.5 2.5 0 0 0-2.5 2.5v9.5A2.5 2.5 0 0 0 7 19.5h9.5A2.5 2.5 0 0 0 19 17v-3.5",
  popin: "M19.5 4.5L12 12M12 7v5h5M10.5 5H7a2.5 2.5 0 0 0-2.5 2.5v9.5A2.5 2.5 0 0 0 7 19.5h9.5A2.5 2.5 0 0 0 19 17v-3.5",
  // The window's sidebar, for showing it whole or as an icon rail.
  sidebar: "M7 4.5h10A2.5 2.5 0 0 1 19.5 7v10a2.5 2.5 0 0 1-2.5 2.5H7A2.5 2.5 0 0 1 4.5 17V7A2.5 2.5 0 0 1 7 4.5zM9.5 4.5v15M6.8 8h.4M6.8 10.5h.4",
  // The file page's context column on the right: show or put away.
  panel: "M7 4.5h10A2.5 2.5 0 0 1 19.5 7v10a2.5 2.5 0 0 1-2.5 2.5H7A2.5 2.5 0 0 1 4.5 17V7A2.5 2.5 0 0 1 7 4.5zM14.5 4.5v15",
  // Playback quality (Quality): a cut stone, the board's ◈.
  quality: "M12 3.5l8.5 8.5-8.5 8.5L3.5 12zM12 8.5l3.5 3.5-3.5 3.5L8.5 12z",
  // Screenshot (Screenshot): a camera body with its lens.
  camera: "M4.5 9A2 2 0 0 1 6.5 7h2l1.2-2h4.6l1.2 2h2a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-11a2 2 0 0 1-2-2zM12 16.2a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4z",
  // Subtitles and A/V sync: two sliders.
  tune: "M4.5 8h9M17.5 8h2M4.5 16h2M10.5 16h9M15.5 5.5v5M8.5 13.5v5",
  // AI Assistant: a four-point sparkle with a small one beside it.
  spark: "M11 5Q11.8 12.2 19 13Q11.8 13.8 11 21Q10.2 13.8 3 13Q10.2 12.2 11 5zM18.5 2.5v4.5M16.25 4.75h4.5",
  arrowRight: "M4.5 12h15M13.5 6l6 6-6 6",
  // AI Assistant: Authorize before sending and its boundaries; Media organization.
  shield: "M12 3.5l7 2.6v5.4c0 4.3-2.8 7.6-7 9.5-4.2-1.9-7-5.2-7-9.5V6.1zM9 12l2 2 4-4",
  film: "M5 4.5h14A1.5 1.5 0 0 1 20.5 6v12a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 18V6A1.5 1.5 0 0 1 5 4.5zM7.5 4.5v15M16.5 4.5v15M3.5 9.5h4M3.5 14.5h4M16.5 9.5h4M16.5 14.5h4",
  arrowLeft: "M19.5 12h-15M10.5 6l-6 6 6 6",
  // Cleanup: three stacked sheets — copies of one file.
  layers: "M12 3.5l8.5 4.5-8.5 4.5L3.5 8zM3.5 12l8.5 4.5 8.5-4.5M3.5 16l8.5 4.5 8.5-4.5",
  // Membership: a three-point crown on its band.
  crown: "M4 8l4.2 3.6L12 5.5l3.8 6.1L20 8l-1.6 9H5.6zM5.6 20h12.8",
  // Music playback: skip to the previous / next track, drawn
  // filled like play; and Collapse, the four corners drawn in.
  skipPrevious: "M6.5 5.5v13h1.6v-13zM18 6.6v10.8a.8.8 0 0 1-1.25.66L9.3 12.66a.8.8 0 0 1 0-1.32l7.45-5.4A.8.8 0 0 1 18 6.6z",
  skipNext: "M17.5 5.5v13h-1.6v-13zM6 6.6v10.8a.8.8 0 0 0 1.25.66l7.45-5.4a.8.8 0 0 0 0-1.32L7.25 5.94A.8.8 0 0 0 6 6.6z",
  collapse: "M4.5 9.5h5v-5M9.5 9.5L4 4M19.5 14.5h-5v5M14.5 14.5L20 20",
  // System notifications: a bell with its clapper.
  bell: "M6.5 16.5V11a5.5 5.5 0 0 1 11 0v5.5l1.5 2h-14zM10 20.5a2.1 2.1 0 0 0 4 0",
};

const props = withDefaults(defineProps<{ name: string; size?: number; filled?: boolean }>(), {
  size: 18,
  filled: false,
});

const d = computed(() => paths[props.name] ?? paths.file);
</script>

<template>
  <svg
    class="icon"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    :fill="filled ? 'currentColor' : 'none'"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="d" />
  </svg>
</template>

<style scoped>
.icon {
  flex: none;
  display: block;
}
</style>
