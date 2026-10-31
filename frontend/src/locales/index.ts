// Every area's messages, keyed by Simplified Chinese source text. Each area file
// belongs to one part of the app; scripts/i18n-check.mjs checks that every
// t()/N_() key has a full entry, that no entry is unused and that two areas
// never translate one key differently.
import type { Messages } from "./types";
import accounts from "./accounts";
import ai from "./ai";
import assistant from "./assistant";
import cleanup from "./cleanup";
import core from "./core";
import files from "./files";
import history from "./history";
import panels from "./panels";
import player from "./player";
import search from "./search";
import settings from "./settings";
import shares from "./shares";
import shell from "./shell";
import store from "./store";
import sync from "./sync";
import transfers from "./transfers";
import updates from "./updates";
import readerCore from "./reader-core";
import readerBook from "./reader-book";
import readerComic from "./reader-comic";
import readerTable from "./reader-table";
import readerOffice from "./reader-office";
import readerPdf from "./reader-pdf";
import readerTimed from "./reader-timed";
import savedsearches from "./savedsearches";
import medialibrary from "./medialibrary";

const areas: Messages[] = [core, shell, accounts, store, settings, ai, assistant, search, files, history, player, transfers, panels, cleanup, shares, sync, updates, readerCore, readerBook, readerComic, readerTable, readerOffice, readerPdf, readerTimed, savedsearches, medialibrary];

const catalog: Messages = Object.assign({}, ...areas);

export default catalog;
