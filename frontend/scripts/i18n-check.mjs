// i18n check (see src/i18n.ts). Run from frontend/:
//
//   node scripts/i18n-check.mjs              every source file, plus unused keys
//   node scripts/i18n-check.mjs src/a.vue …  only those files' text and keys
//
// It fails on:
// - Chinese, Japanese or Korean text in a .ts/.vue source that is not the
//   literal first argument of t() or N_(): template text, static attributes,
//   strings in script and template expressions. Comments are not text. A line
//   whose text or previous line says `i18n-ignore` is skipped, and a file
//   that says `i18n-ignore-file` anywhere (grammar, model prompts) entirely.
//   Object keys and string literal types are data, not text.
// - a t()/N_() key built from a template with ${}: keys must be static.
// - a key with no entry in src/locales, an entry missing a language or
//   empty, a translation whose {params} differ from the key's, a key two
//   areas translate differently and (full run) an entry nothing uses.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";
import { parse as parseSFC } from "vue/compiler-sfc";

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const src = join(frontend, "src");
const locales = join(src, "locales");
const CJK = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;
const CALLS = ["t", "N_"];
const FIELDS = ["tw", "en", "ja", "ko"];
const CONTEXT = /^[a-z][a-z0-9-]*\|/;

function sources(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      if (path !== locales && name !== "test") out.push(...sources(path));
    } else if (/\.(ts|vue)$/.test(name) && !/\.test\.ts$|\.d\.ts$/.test(name) && path !== join(src, "i18n.ts")) {
      out.push(path);
    }
  }
  return out;
}

/** scan reports CJK text outside t()/N_() in one file and collects its keys. */
function scan(file, problems, keys) {
  const text = readFileSync(file, "utf8");
  const name = relative(frontend, file);
  const lines = text.split("\n");
  const checkText = !text.includes("i18n-ignore-file");
  const ignored = (line) => lines[line - 1]?.includes("i18n-ignore") || lines[line - 2]?.includes("i18n-ignore");
  const report = (line, what) => {
    if (checkText && !ignored(line)) problems.push(`${name}:${line}: ${what.replace(/\s+/g, " ").trim().slice(0, 120)}`);
  };

  const scanCode = (code, lineOffset) => {
    const sf = ts.createSourceFile("x.ts", code, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const lineOf = (node) => sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1 + lineOffset;
    const isKeyCall = (node) => ts.isCallExpression(node) && ts.isIdentifier(node.expression) && CALLS.includes(node.expression.text);
    const visit = (node) => {
      if (isKeyCall(node)) {
        const arg = node.arguments[0];
        if (arg && (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg))) keys.push({ key: arg.text, where: `${name}:${lineOf(arg)}` });
        else if (arg && ts.isTemplateExpression(arg)) problems.push(`${name}:${lineOf(arg)}: ${node.expression.text}() key must be static, not \`…\${}…\``);
      }
      let literal = null;
      if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) literal = node.text;
      else if (ts.isTemplateExpression(node)) literal = node.head.text + node.templateSpans.map((s) => s.literal.text).join("");
      if (literal !== null && CJK.test(literal)) {
        const parent = node.parent;
        const allowed =
          (isKeyCall(parent) && parent.arguments[0] === node && !ts.isTemplateExpression(node)) ||
          ((ts.isPropertyAssignment(parent) || ts.isPropertySignature(parent)) && parent.name === node) ||
          ts.isLiteralTypeNode(parent) ||
          ts.isImportDeclaration(parent) ||
          ts.isExportDeclaration(parent);
        if (!allowed) report(lineOf(node), node.getText(sf));
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
  };

  if (file.endsWith(".ts")) {
    scanCode(text, 0);
    return;
  }
  const { descriptor } = parseSFC(text, { filename: file });
  for (const block of [descriptor.script, descriptor.scriptSetup]) {
    if (block) scanCode(block.content, block.loc.start.line - 1);
  }
  const expr = (e, statements) => {
    if (e && e.content) scanCode(statements ? e.content : `(${e.content})`, e.loc.start.line - 1);
  };
  // compiler-core's untransformed AST: 1 element, 2 text, 5 interpolation,
  // 6 static attribute, 7 directive.
  const walk = (node) => {
    if (node.type === 2 && CJK.test(node.content)) report(node.loc.start.line, node.content);
    if (node.type === 5) expr(node.content, false);
    if (node.type === 1) {
      for (const p of node.props) {
        if (p.type === 6 && p.value && CJK.test(p.value.content)) report(p.loc.start.line, `${p.name}="${p.value.content}"`);
        if (p.type === 7) expr(p.exp, p.name === "on");
      }
    }
    for (const child of node.children ?? []) walk(child);
  };
  if (descriptor.template?.ast) walk(descriptor.template.ast);
}

/** placeholders lists a message's {params}. */
function placeholders(text) {
  return [...new Set([...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]))].sort().join(",");
}

export async function check(only = []) {
  const problems = [];
  const keys = [];
  const files = only.length ? only.map((f) => resolve(frontend, f)) : sources(src);
  for (const file of files) scan(file, problems, keys);

  const catalog = new Map();
  for (const name of readdirSync(locales).sort()) {
    if (!name.endsWith(".ts") || name === "index.ts" || name === "types.ts") continue;
    const area = (await import(pathToFileURL(join(locales, name)).href)).default;
    for (const [key, entry] of Object.entries(area)) {
      const where = `src/locales/${name}`;
      const source = key.replace(CONTEXT, "");
      for (const field of FIELDS) {
        const value = entry?.[field];
        if (typeof value === "string") {
          if (!value.trim()) problems.push(`${where}: "${key}" has an empty ${field}`);
          else if (placeholders(value) !== placeholders(source)) problems.push(`${where}: "${key}" ${field} params {${placeholders(value)}} ≠ {${placeholders(source)}}`);
        } else if (typeof value === "function") {
          const sample = Object.fromEntries(placeholders(source).split(",").filter(Boolean).map((p) => [p, "2"]));
          const out = value(sample);
          if (typeof out !== "string" || !out.trim()) problems.push(`${where}: "${key}" ${field} function returns nothing`);
        } else {
          problems.push(`${where}: "${key}" has no ${field}`);
        }
      }
      const seen = catalog.get(key);
      const shape = JSON.stringify(entry, (_, v) => (typeof v === "function" ? v.toString() : v));
      if (seen && seen.shape !== shape) problems.push(`${where}: "${key}" is also in ${seen.where} with other translations`);
      if (!seen) catalog.set(key, { where, shape });
    }
  }

  const missing = new Map();
  for (const { key, where } of keys) if (!catalog.has(key) && !missing.has(key)) missing.set(key, where);
  for (const [key, where] of missing) problems.push(`${where}: no catalog entry for "${key}"`);
  if (!only.length) {
    const used = new Set(keys.map((k) => k.key));
    for (const [key, { where }] of catalog) if (!used.has(key)) problems.push(`${where}: "${key}" is not used`);
  }
  return { problems, keys: keys.length, entries: catalog.size };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { problems, keys, entries } = await check(process.argv.slice(2));
  for (const p of problems) console.log(p);
  console.log(`${problems.length} problem(s); ${keys} key uses, ${entries} catalog entries`);
  process.exit(problems.length ? 1 : 0);
}
