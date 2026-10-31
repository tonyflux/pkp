import type { Card, CardRow, FilesCard, SearchResultGroup } from "./agent";
import { bytes, shownPath } from "./format";
import { t } from "./i18n";
import { emptyQuery, extList, PLAYED_LABEL, type AIQuery } from "./searchquery";

export function searchSources(cards: readonly Card[]): FilesCard[] {
  return cards.filter((card): card is FilesCard => card.kind === "files" && card.query != null);
}

export function hasSearchResults(card: FilesCard): boolean {
  return card.rows.length > 0 || card.aggregate.some((row) => row.count > 0);
}

export interface PresentedGroup {
  card: FilesCard;
  sources: FilesCard[];
  unique: boolean;
}

/** Presentation only: original candidates, totals and aggregate rows stay untouched. */
export function presentSearchGroups(cards: readonly Card[], groups: readonly SearchResultGroup[] | undefined, live: boolean): PresentedGroup[] {
  const sources = searchSources(cards);
  if (groups === undefined) {
    if (live || sources.length !== 1 || !hasSearchResults(sources[0])) return [];
    const source = sources[0];
    if (source.aggregate.length || source.query?.aggregate) return [{ card: source, sources, unique: false }];
    const rows = [...new Map(source.rows.map((row) => [row.id, row])).values()];
    return [{ card: { ...source, rows }, sources, unique: true }];
  }
  const byId = new Map(sources.map((source) => [source.id, source]));
  return groups.flatMap((group, index): PresentedGroup[] => {
    const selected = [...new Set(group.sourceIds)].map((id) => byId.get(id));
    if (!selected.length || selected.some((source) => !source)) return [];
    // Keep source chronology so later search metadata wins, independent of selection order.
    const chosenIds = new Set(group.sourceIds);
    const chosen = sources.filter((source) => chosenIds.has(source.id));
    const aggregate = chosen.filter((source) => source.aggregate.length > 0 || source.query?.aggregate);
    if (aggregate.length) {
      if (chosen.length !== 1 || group.fileIds !== undefined || !hasSearchResults(chosen[0])) return [];
      return [{ card: { ...chosen[0], title: group.title }, sources: chosen, unique: false }];
    }
    const wanted = group.fileIds === undefined ? null : new Set(group.fileIds);
    const rows = new Map<string, CardRow>();
    for (const source of chosen) {
      for (const row of source.rows) if (!wanted || wanted.has(row.id)) rows.set(row.id, row);
    }
    if (!rows.size) return [];
    return [{
      card: { ...chosen[0], id: `result-${index}`, title: group.title, rows: [...rows.values()], total: rows.size, aggregate: [], note: "" },
      sources: chosen,
      unique: true,
    }];
  });
}

/** Persisted queries may predate fields added to AIQuery. */
export function querySummary(query: Partial<AIQuery> | null, title = ""): string {
  if (!query || typeof query !== "object") return title;
  const q = emptyQuery();
  q.keywords = Array.isArray(query.keywords) ? query.keywords.filter((value) => typeof value === "string" && value.trim()) : [];
  q.ext = Array.isArray(query.ext) ? query.ext.filter((value) => typeof value === "string" && value.trim()) : [];
  for (const field of ["category", "under", "starred", "kind", "sort"] as const) {
    const value = query[field];
    if (typeof value === "string") q[field] = value;
  }
  for (const field of ["createdAfter", "createdBefore", "modifiedAfter", "modifiedBefore"] as const) {
    const value = query[field];
    if (typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value)) q[field] = value;
  }
  for (const field of ["minSize", "maxSize", "limit"] as const) {
    const value = query[field];
    if (typeof value === "number" && Number.isFinite(value) && value > 0) q[field] = value;
  }
  if (query.played && ["any", "unfinished", "finished"].includes(query.played)) q.played = query.played;
  q.asc = query.asc === true;
  const aggregate = query.aggregate;
  if (aggregate && typeof aggregate === "object" && ["folder", "category", "ext"].includes(aggregate.by)) {
    q.aggregate = {
      by: aggregate.by,
      measure: aggregate.measure === "count" ? "count" : "size",
      limit: typeof aggregate.limit === "number" && Number.isFinite(aggregate.limit) && aggregate.limit > 0 ? aggregate.limit : 10,
    };
  }
  const parts: string[] = [];
  if (q.keywords?.length) parts.push(t("关键词：{v}", { v: extList(q.keywords) }));
  if (q.ext?.length) parts.push(t("扩展名：{v}", { v: extList(q.ext) }));
  const category = { video: t("视频"), image: t("图片"), audio: t("音频"), document: t("文档"), archive: t("压缩包"), other: t("其他") };
  if (q.category && Object.prototype.hasOwnProperty.call(category, q.category)) parts.push(t("类型：{v}", { v: category[q.category as keyof typeof category] }));
  if (q.minSize) parts.push(t("大小：{v}", { v: `≥ ${bytes(q.minSize)}` }));
  if (q.maxSize) parts.push(t("大小：{v}", { v: `≤ ${bytes(q.maxSize)}` }));
  for (const field of ["created", "modified"] as const) {
    const after = q[field === "created" ? "createdAfter" : "modifiedAfter"];
    const before = q[field === "created" ? "createdBefore" : "modifiedBefore"];
    if (!after && !before) continue;
    const value = after && before ? (after === before ? after : t("{a} 至 {b}", { a: after, b: before }))
      : after ? t("{d} 之后", { d: after }) : t("{d} 之前", { d: before });
    parts.push(field === "created" ? t("保存时间：{v}", { v: value }) : t("修改时间：{v}", { v: value }));
  }
  parts.push(t("在：{v}", { v: q.under ? shownPath(q.under) : t("全部文件") }));
  if (Array.isArray(query.ids)) parts.push(t("已选 {n} 项", { n: query.ids.length }));
  if (q.starred === "yes") parts.push(t("只看星标"));
  if (q.starred === "no") parts.push(t("不含星标"));
  if (q.played) parts.push(t("播放：{v}", { v: PLAYED_LABEL[q.played] ?? q.played }));
  if (q.kind === "folder") parts.push(t("只看文件夹"));
  if (q.kind === "file") parts.push(t("只看文件"));
  const sorts = { modified: t("修改时间"), created: t("保存时间"), size: t("大小"), name: t("名称"), played: t("最近播放") };
  parts.push(t("排序：{v}", { v: `${Object.prototype.hasOwnProperty.call(sorts, q.sort) ? sorts[q.sort as keyof typeof sorts] : sorts.modified} ${q.asc ? "↑" : "↓"}` }));
  parts.push(t("最多 {n} 项", { n: q.limit || 50 }));
  if (q.aggregate) {
    const by = { folder: t("文件夹"), category: t("类型"), ext: t("扩展名") };
    parts.push(t("统计：{by} · {measure} · 最多 {n} 组", {
      by: by[q.aggregate.by as keyof typeof by] ?? q.aggregate.by,
      measure: q.aggregate.measure === "count" ? t("数量") : t("大小"), n: q.aggregate.limit || 10,
    }));
  }
  return parts.join(" · ");
}
