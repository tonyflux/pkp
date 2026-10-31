import { markRaw, reactive } from "vue";
import { DriveService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { Entry, RecentPlay } from "../bindings/github.com/tonyflux/pkp/internal/services";
import { splitName, titleKey, versionTag, type MediaGroup } from "./media";
import { parseGroupsSteps } from "./parse";
import { playFinished, playHasProgress } from "./plays";
import { t } from "./i18n";

export interface LibraryAncestor { id: string; name: string }
export interface LibraryFile { entry: Entry; ancestors: LibraryAncestor[] }
export interface LibraryOverride { workId: string; title: string; type: "movie" | "series"; year: number; season: number; episode: number }
export interface LibraryVersion {
  entry: Entry;
  ancestors: readonly LibraryAncestor[];
  version: string;
  history: RecentPlay | null;
  finished: boolean;
  progress: boolean;
}
export interface LibraryEpisode { key: string; season: number; episode: number; versions: LibraryVersion[]; finished: boolean; progress: boolean }
export interface LibraryWork {
  key: string;
  title: string;
  type: "movie" | "series" | "unknown";
  year: number;
  manual: boolean;
  episodes: LibraryEpisode[];
  finished: boolean;
  progress: boolean;
}
export interface LibraryScope { folderId?: string; starred?: boolean }
const natural = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

function singleton(file: LibraryFile, id: string): MediaGroup {
  const { ext } = splitName(file.entry.name);
  const media = { id: file.entry.id, name: file.entry.name, dir: file.ancestors.map((a) => a.name), ext: ext.slice(1).toLowerCase(), size: file.entry.size, modified: file.entry.modifiedTime, numbers: [] };
  return { id, kind: "video", pattern: file.entry.name, files: [media], dirs: [media.dir], exts: { [media.ext]: 1 }, size: { min: media.size, max: media.size }, variables: [], samples: [media] };
}

function ancestryKey(file: LibraryFile, title: string, series: boolean): string {
  if (!series) return file.entry.parentId;
  const key = titleKey(title);
  // A named work folder is an identity boundary, including two same-named folders.
  for (let i = file.ancestors.length - 1; i >= 0; i--) {
    const ancestor = file.ancestors[i];
    const name = titleKey(ancestor.name);
    if (key.length > 1 && name.includes(key)) return ancestor.id;
  }
  const last = file.ancestors.at(-1);
  // Season folders without a named work folder still share only their real parent.
  if (last && /^(?:season[ ._-]*\d+|s\d+|第[\d一二三四五六七八九十零〇]+季|specials?)$/iu.test(last.name.trim())) { // i18n-ignore: filename grammar
    return file.ancestors.at(-2)?.id ?? "";
  }
  return file.entry.parentId;
}

/** Every file is read literally: varying years/versions can never become episode variables. */
export function groupLibrary(files: readonly LibraryFile[], histories: readonly RecentPlay[], overrides: Readonly<Record<string, LibraryOverride>> = {}): LibraryWork[] {
  const steps = groupLibrarySteps(files, histories, overrides);
  for (;;) {
    const step = steps.next();
    if (step.done) return step.value;
  }
}

function* groupLibrarySteps(files: readonly LibraryFile[], histories: readonly RecentPlay[], overrides: Readonly<Record<string, LibraryOverride>>): Generator<void, LibraryWork[]> {
  const history = new Map<string, RecentPlay>();
  for (const record of histories) {
    const old = history.get(record.fileId);
    if (!old || record.playedAt > old.playedAt) history.set(record.fileId, record);
  }
  const groups: MediaGroup[] = [];
  for (let i = 0; i < files.length; i++) {
    groups.push(singleton(files[i], String(i)));
    if (i % 64 === 63) yield;
  }
  const parsed = yield* parseGroupsSteps(groups, "");
  const works = new Map<string, LibraryWork & { byEpisode: Map<string, LibraryEpisode> }>();
  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    const override = Object.prototype.hasOwnProperty.call(overrides, file.entry.id) ? overrides[file.entry.id] : undefined;
    const identity = parsed[i].confidence === "high" ? parsed[i].identity : null;
    let type: LibraryWork["type"] = "unknown";
    if (override) type = override.type;
    else if (identity?.type === "movie") type = "movie";
    else if (identity?.type === "series" || identity?.type === "special") type = "series";
    const title = override?.title ?? (type === "unknown" ? file.entry.name : identity!.title);
    const year = override?.year ?? identity?.year ?? 0;
    const season = type === "series" ? override?.season ?? identity?.season ?? 1 : 0;
    const episode = type === "series" ? override?.episode ?? identity?.episode ?? 0 : 0;
    // Unknown identities and unnumbered episodes are never coalesced.
    const boundary = override ? ["manual", override.workId] : type === "unknown" || type === "movie" && !year || type === "series" && !episode ? ["file", file.entry.id] : ["rules", ancestryKey(file, title, type === "series")];
    const key = JSON.stringify([...boundary, type, titleKey(title), year]);
    let work = works.get(key);
    if (!work) {
      work = { key, title, type, year, manual: !!override, episodes: [], byEpisode: new Map(), finished: false, progress: false };
      works.set(key, work);
    }
    const episodeKey = `${season}:${episode}`;
    let item = work.byEpisode.get(episodeKey);
    if (!item) {
      item = { key: episodeKey, season, episode, versions: [], finished: false, progress: false };
      work.byEpisode.set(episodeKey, item);
      work.episodes.push(item);
    }
    const record = history.get(file.entry.id) ?? null;
    item.versions.push({ entry: file.entry, ancestors: file.ancestors, version: versionTag(file.entry.name), history: record, finished: !!record && playFinished(record.position, record.duration), progress: !!record && playHasProgress(record.position, record.duration) && !playFinished(record.position, record.duration) });
    if (i % 64 === 63) yield;
  }
  const out: LibraryWork[] = [];
  for (const work of works.values()) {
    work.episodes.sort((a, b) => a.season - b.season || a.episode - b.episode);
    for (const episode of work.episodes) {
      episode.versions.sort((a, b) => natural.compare(a.entry.name, b.entry.name) || a.entry.id.localeCompare(b.entry.id));
      episode.finished = episode.versions.some((v) => v.finished);
      episode.progress = !episode.finished && episode.versions.some((v) => v.progress);
      yield;
    }
    work.finished = work.episodes.every((e) => e.finished);
    work.progress = !work.finished && work.episodes.some((e) => e.finished || e.progress);
    const { byEpisode: _, ...value } = work;
    out.push(value);
    yield;
  }
  const compare = (a: LibraryWork, b: LibraryWork) => natural.compare(a.title, b.title) || a.year - b.year || a.key.localeCompare(b.key);
  // Bound native sort calls too: a drive full of unrelated videos can have one work per file.
  for (let start = 0; start < out.length; start += 256) {
    out.splice(start, Math.min(256, out.length - start), ...out.slice(start, start + 256).sort(compare));
    yield;
  }
  let sorted = out;
  let buffer = new Array<LibraryWork>(out.length);
  for (let width = 256; width < out.length; width *= 2) {
    for (let start = 0; start < out.length; start += width * 2) {
      const middle = Math.min(start + width, out.length);
      const end = Math.min(start + width * 2, out.length);
      let left = start;
      let right = middle;
      for (let i = start; i < end; i++) {
        buffer[i] = right >= end || left < middle && compare(sorted[left], sorted[right]) <= 0 ? sorted[left++] : sorted[right++];
        if (i % 64 === 63) yield;
      }
    }
    [sorted, buffer] = [buffer, sorted];
  }
  return sorted;
}

async function groupLibraryAsync(files: readonly LibraryFile[], histories: readonly RecentPlay[], overrides: Readonly<Record<string, LibraryOverride>>, current: () => boolean): Promise<LibraryWork[] | null> {
  const steps = groupLibrarySteps(files, histories, overrides);
  let deadline = performance.now() + 8;
  while (current()) {
    const step = steps.next();
    if (step.done) return step.value;
    if (performance.now() >= deadline) {
      // macOS 12 WebKit has no scheduler.yield or Promise.withResolvers.
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      deadline = performance.now() + 8;
    }
  }
  return null;
}

/** Filter individual versions first, so a folder/search view never plays a hidden sibling. */
export function filterLibrary(works: readonly LibraryWork[], query: string, scope: LibraryScope = {}): LibraryWork[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle && !scope.folderId && !scope.starred) return works.slice();
  const result: LibraryWork[] = [];
  for (const work of works) {
    const episodes: LibraryEpisode[] = [];
    for (const episode of work.episodes) {
      const versions = episode.versions.filter((version) =>
        (!scope.folderId || version.entry.parentId === scope.folderId || version.ancestors.some((ancestor) => ancestor.id === scope.folderId)) &&
        (!scope.starred || version.entry.starred) &&
        (!needle || version.entry.name.toLocaleLowerCase().includes(needle)));
      if (!versions.length) continue;
      const finished = versions.some((version) => version.finished);
      episodes.push({ ...episode, versions, finished, progress: !finished && versions.some((version) => version.progress) });
    }
    if (!episodes.length) continue;
    const finished = episodes.every((episode) => episode.finished);
    result.push({ ...work, episodes, finished, progress: !finished && episodes.some((episode) => episode.finished || episode.progress) });
  }
  return result;
}

export function preferredVersion(episode: LibraryEpisode): LibraryVersion {
  return episode.versions.filter((v) => v.progress).sort((a, b) => (b.history?.playedAt ?? "").localeCompare(a.history?.playedAt ?? ""))[0] ?? episode.versions[0];
}

export function continueLibrary(work: LibraryWork): LibraryVersion {
  const unfinished = work.episodes.filter((e) => !e.finished);
  const active = unfinished.flatMap((e) => e.versions.filter((v) => v.progress)).sort((a, b) => (b.history?.playedAt ?? "").localeCompare(a.history?.playedAt ?? ""));
  return active[0] ?? preferredVersion(unfinished[0] ?? work.episodes[0]);
}

/** One version per episode; the explicitly selected copy wins its own slot. */
export function libraryQueue(work: LibraryWork, selected: LibraryVersion): Entry[] {
  return work.episodes.map((episode) => episode.versions.find((v) => v.entry.id === selected.entry.id) ?? preferredVersion(episode)).map((v) => v.entry);
}

export const mediaLibrary = reactive({
  account: "", files: [] as LibraryFile[], history: [] as RecentPlay[], overrides: {} as Record<string, LibraryOverride>, works: [] as LibraryWork[],
  loading: false, ready: false, loaded: false, total: 0, read: 0, historyLoading: false, error: "", historyError: "", revision: "", sourceRevision: "",
});
let serial = 0;
let activeLoad: { account: string; revision: string; promise: Promise<void> } | null = null;

/** Keep completed snapshots, but do not parse a hidden view or publish its late replies. */
export function cancelMediaLibraryLoad(): void {
  if (!mediaLibrary.loading) return;
  serial++;
  activeLoad = null;
  mediaLibrary.loading = false;
  mediaLibrary.historyLoading = false;
}

export function dropAccountMediaLibrary(account: string) {
  if (mediaLibrary.account !== account) return;
  serial++;
  activeLoad = null;
  Object.assign(mediaLibrary, { account: "", files: [], history: [], overrides: {}, works: [], loading: false, ready: false, loaded: false, total: 0, read: 0, historyLoading: false, error: "", historyError: "", revision: "", sourceRevision: "" });
}

/** History enriches already displayed work objects without parsing every filename again. */
function applyLibraryHistory(rows: RecentPlay[]): void {
  mediaLibrary.history = markRaw(rows);
  const history = new Map(rows.map((row) => [row.fileId, row]));
  for (const work of mediaLibrary.works) {
    for (const episode of work.episodes) {
      for (const version of episode.versions) {
        const record = history.get(version.entry.id) ?? null;
        version.history = record;
        version.finished = !!record && playFinished(record.position, record.duration);
        version.progress = !!record && playHasProgress(record.position, record.duration) && !version.finished;
      }
      episode.finished = episode.versions.some((version) => version.finished);
      episode.progress = !episode.finished && episode.versions.some((version) => version.progress);
    }
    work.finished = work.episodes.every((episode) => episode.finished);
    work.progress = !work.finished && work.episodes.some((episode) => episode.finished || episode.progress);
  }
  mediaLibrary.works = markRaw(mediaLibrary.works.slice());
}

export function loadMediaLibrary(account: string, refresh = true, revision = mediaLibrary.sourceRevision): Promise<void> {
  if (account !== mediaLibrary.account) {
    dropAccountMediaLibrary(mediaLibrary.account);
    mediaLibrary.account = account;
  }
  if (!account) return Promise.resolve();
  if (activeLoad?.account === account && activeLoad.revision === revision) return activeLoad.promise;
  if (!refresh && mediaLibrary.loaded && mediaLibrary.sourceRevision === revision && !mediaLibrary.error) return Promise.resolve();
  const request = ++serial;
  const promise = readMediaLibrary(account, request, revision);
  activeLoad = { account, revision, promise };
  void promise.finally(() => { if (activeLoad?.promise === promise) activeLoad = null; });
  return promise;
}

async function readMediaLibrary(account: string, request: number, sourceRevision: string): Promise<void> {
  const current = () => request === serial && mediaLibrary.account === account;
  mediaLibrary.loading = true;
  mediaLibrary.loaded = false;
  mediaLibrary.historyLoading = true;
  mediaLibrary.error = "";
  mediaLibrary.historyError = "";
  mediaLibrary.read = 0;
  const historyPromise = DriveService.MediaLibraryHistory(account).then((rows) => {
    if (current()) applyLibraryHistory(rows ?? []);
  }, (error: unknown) => {
    if (current()) mediaLibrary.historyError = String(error);
  }).finally(() => { if (current()) mediaLibrary.historyLoading = false; });
  // Cloud history never gates local files, progress, or the first card.
  void historyPromise;
  try {
    const [saved, local] = await Promise.all([DriveService.MediaLibraryOverrides(account), DriveService.LocalPlayHistory(account)]);
    if (!current()) return;
    const overrides = saved as Record<string, LibraryOverride>;
    mediaLibrary.overrides = overrides;
    if (mediaLibrary.historyLoading || mediaLibrary.historyError) mediaLibrary.history = markRaw(local ?? []);
    let after = "";
    let revision: string | null = null;
    let total = 0;
    const files: LibraryFile[] = [];
    const seen = new Set<string>();
    let previewed = false;
    for (;;) {
      const page = await DriveService.MediaLibraryPage(account, after, previewed ? 2000 : 500);
      if (!current()) return;
      if (page.account !== account) throw new Error(t("媒体库账号已变化，请重试"));
      if (!page.ready) {
        Object.assign(mediaLibrary, { ready: false, loaded: true, files: [], works: [], total: 0, read: 0 });
        return;
      }
      if (revision !== null && (revision !== page.revision || total !== page.total)) throw new Error(t("媒体库索引已变化，请刷新"));
      revision = page.revision;
      total = page.total;
      for (const file of page.files ?? []) {
        if (!seen.has(file.entry.id)) { seen.add(file.entry.id); files.push({ entry: file.entry, ancestors: file.ancestors ?? [] }); }
      }
      mediaLibrary.read = files.length;
      mediaLibrary.total = total;
      if (!previewed && page.next) {
        const works = await groupLibraryAsync(files, mediaLibrary.history, overrides, current);
        if (!current() || !works) return;
        mediaLibrary.works = markRaw(works);
        applyLibraryHistory(mediaLibrary.history);
        mediaLibrary.ready = true;
        previewed = true;
        // macOS 12 WebKit lacks Promise.withResolvers; yield with its native Promise API.
        await new Promise<void>((resolve) => setTimeout(resolve, 0));
        if (!current()) return;
      }
      if (!page.next) break;
      if (page.next <= after) throw new Error(t("媒体库索引已变化，请刷新"));
      after = page.next;
    }
    if (files.length !== total) throw new Error(t("媒体库索引已变化，请刷新"));
    if (!current()) return;
    const works = await groupLibraryAsync(files, mediaLibrary.history, overrides, current);
    if (!current() || !works) return;
    Object.assign(mediaLibrary, { files: markRaw(files), works: markRaw(works), total, ready: true, loaded: true, revision: revision ?? "", sourceRevision });
    applyLibraryHistory(mediaLibrary.history);
  } catch (error) {
    if (current()) mediaLibrary.error = String(error);
  } finally {
    if (current()) mediaLibrary.loading = false;
  }
}

export async function saveLibraryOverride(account: string, fileId: string, value: LibraryOverride | null): Promise<boolean> {
  if (account !== mediaLibrary.account) return false;
  const request = serial;
  await DriveService.SetMediaLibraryOverride(account, fileId, value);
  const current = () => account === mediaLibrary.account && request === serial;
  if (!current()) return false;
  if (value) mediaLibrary.overrides[fileId] = value;
  else delete mediaLibrary.overrides[fileId];
  const works = await groupLibraryAsync(mediaLibrary.files, mediaLibrary.history, mediaLibrary.overrides, current);
  if (!current() || !works) return false;
  mediaLibrary.works = markRaw(works);
  applyLibraryHistory(mediaLibrary.history);
  return true;
}
