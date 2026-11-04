import { readFile, readdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const lock = JSON.parse(await readFile(resolve(root, "package-lock.json"), "utf8"));
const notices = ["PKP frontend third-party licenses\n"];
for (const [path, entry] of Object.entries(lock.packages)) {
  if (!path || entry.dev || entry.devOptional || !path.startsWith("node_modules/")) continue;
  const directory = resolve(root, path);
  let files;
  try { files = await readdir(directory); } catch { if (entry.optional) continue; throw new Error(`Missing production dependency: ${path}`); }
  const manifest = JSON.parse(await readFile(resolve(directory, "package.json"), "utf8"));
  const licenses = files.filter(name => /^(?:licen[cs]e|copying|notice)(?:[.-]|$)/i.test(name));
  notices.push(`\n${"=".repeat(72)}\n${manifest.name} ${manifest.version}\nLicense: ${manifest.license ?? entry.license ?? "See package license"}\n`);
  for (const name of licenses) notices.push(`\n--- ${name} ---\n${await readFile(resolve(directory, name), "utf8")}`);
  if (manifest.name === "isarray" && manifest.version === "1.0.0") {
    const readme = await readFile(resolve(directory, "README.md"), "utf8");
    const section = readme.split("## License\n")[1];
    if (!section) throw new Error("Missing isarray 1.0.0 README license");
    notices.push(`\n--- README.md: License ---\n${section}`);
  }
  if (manifest.name === "qrcode-generator" && manifest.version === "1.4.4") {
    const source = await readFile(resolve(directory, "qrcode.js"), "utf8");
    notices.push(`\n--- qrcode.js notice ---\n${source.split("\nvar qrcode =")[0]}

--- MIT license text (https://opensource.org/licenses/mit-license.php) ---
MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
associated documentation files (the "Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the
following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial
portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT
LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO
EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE
USE OR OTHER DEALINGS IN THE SOFTWARE.
`);
  }
  if (manifest.name.startsWith("@napi-rs/canvas-") && manifest.version === "0.1.100") {
    notices.push(`\n--- upstream canvas v0.1.100 LICENSE ---\n${await readFile(resolve(root, "scripts/canvas-0.1.100-LICENSE.txt"), "utf8")}`);
  }
}

for (const folder of ["cmaps", "standard_fonts", "wasm"]) {
  const directory = resolve(root, "node_modules/pdfjs-dist", folder);
  for (const name of (await readdir(directory)).filter(name => /^LICENSE/.test(name))) {
    notices.push(`\n--- pdfjs-dist/${folder}/${name} ---\n${await readFile(resolve(directory, name), "utf8")}`);
  }
}
await writeFile(resolve(root, "dist/THIRD_PARTY_LICENSES.txt"), notices.join("\n"));