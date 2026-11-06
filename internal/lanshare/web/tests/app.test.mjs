import test from 'node:test';
import assert from 'node:assert/strict';
import { escapeHTML, isImage, isMedia, activeTask, taskPercent, contentURL, probeCapabilities, supportsMedia } from '../app.js';
import { languages, messages, detectLanguage, translate } from '../locales.js';

test('all visitor languages have every translated message', () => {
  const keys = Object.keys(messages.en).sort();
  for (const language of Object.keys(languages)) {
    assert.deepEqual(Object.keys(messages[language]).sort(), keys);
    for (const key of keys) assert.equal(typeof messages[language][key], 'string', `${language}: ${key}`);
  }
  assert.equal(detectLanguage(['zh-HK', 'en-US']), 'zh-Hant');
  assert.equal(detectLanguage(['zh-SG']), 'zh-Hans');
  assert.equal(detectLanguage(['ja-JP']), 'ja');
  assert.equal(detectLanguage(['ko-KR']), 'ko');
  assert.equal(detectLanguage(['fr-FR', 'en-GB']), 'en');
  assert.equal(translate('ja', 'prepare_disabled'), messages.ja.prepareDisabled);
  assert.equal(translate('ko', 'unsupported_media'), messages.ko.unsupported);
  assert.equal(translate('en', 'untrusted server error details'), messages.en.internal);
});

test('untrusted names and opaque IDs stay data, and originals remain distinct', () => {
  assert.equal(escapeHTML('<img src=x onerror="alert(1)">&\''), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;&amp;&#39;');
  const url = new URL(contentURL('space&x=1', 'file?/#秘密', 'original', true), 'http://localhost');
  assert.equal(url.searchParams.get('space'), 'space&x=1');
  assert.equal(url.searchParams.get('id'), 'file?/#秘密');
  assert.equal(url.searchParams.get('download'), '1');
  assert.equal(url.searchParams.get('variant'), 'original');
  assert.equal(new URL(contentURL('s', 'f', 'compatible'), 'http://localhost').searchParams.get('download'), '0');
  assert.equal(isImage({ mime: 'image/svg+xml' }), false);
  assert.equal(isImage({ mime: 'text/html', name: 'picture.png' }), false);
  assert.equal(isImage({ mime: 'image/webp' }), true);
  assert.equal(isMedia({ name: 'movie.mkv', mime: 'application/octet-stream' }), true);
});

test('task UI never invents progress or treats queued output as ready', () => {
  assert.equal(taskPercent({ done: 5, total: 0 }), null);
  assert.equal(taskPercent({ total: 100 }), null);
  assert.equal(taskPercent({ done: -1, total: 100 }), null);
  assert.equal(taskPercent({ done: 25, total: 100 }), 25);
  assert.equal(taskPercent({ done: 120, total: 100 }), 100);
  assert.equal(activeTask({ state: 'held' }), true);
  assert.equal(activeTask({ state: 'ready' }), false);
  assert.equal(activeTask({ state: 'cancelled' }), false);
});

function environment(canPlayType, decodingInfo) {
  return { document: { createElement: () => ({ canPlayType }) }, navigator: { mediaCapabilities: decodingInfo ? { decodingInfo } : undefined }, matchMedia: () => ({ matches: false }) };
}

test('source-specific rejection does not disable generic conversion output', async () => {
  const calls = [];
  const info = { videoCodec: 'h264', videoMime: 'video/mp4; codecs="avc1.6e0033"', width: 3840, height: 2160, videoBitrate: 20000000, frameRate: 24 };
  const caps = await probeCapabilities(info, environment(() => 'probably', async configuration => { calls.push(configuration); return { supported: false }; }));
  assert.equal(caps.h264, true);
  assert.equal(caps.videoSupported, false);
  assert.equal(caps.hdr, false);
  assert.equal(calls[0].video.framerate, 24);
  assert.equal(calls[0].video.bitrate, 20000000);
});

test('missing real bitrate or frame rate uses canPlayType without invented probe parameters', async () => {
  let calls = 0;
  const caps = await probeCapabilities({ videoCodec: 'h264', videoMime: 'actual-profile' }, environment(mime => mime === 'actual-profile' ? '' : 'maybe', async () => { calls++; return { supported: true }; }));
  assert.equal(calls, 0);
  assert.equal(caps.h264, true);
  assert.equal(caps.videoSupported, false);
});

test('cached compatible copies require this browser to support their actual tracks and container', () => {
  const info = { mime: 'video/mp4', videoCodec: 'hevc', bitDepth: 10, audioCodec: 'aac' };
  const caps = { mp4: true, hevc: true, hevc10: false, aac: true };
  assert.equal(supportsMedia(info, caps), false);
  assert.equal(supportsMedia(info, { ...caps, hevc10: true }), true);
  assert.equal(supportsMedia(info, { ...caps, hevc10: true, videoSupported: false }), false);
  assert.equal(supportsMedia({ ...info, hdr: true }, { ...caps, hevc10: true }), false);
  assert.equal(supportsMedia({ mime: 'video/webm', videoCodec: 'vp9', audioCodec: 'opus' }, { webm: true, vp9: true, opus: true }), true);
  assert.equal(supportsMedia({ mime: 'video/mp4', videoCodec: 'unknown' }, { mp4: true }), false);
});
