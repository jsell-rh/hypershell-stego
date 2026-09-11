// This Node fixture supplies browser cookie and Origin behavior for protocol tests.
// The generated client receives no cookie or OAuth credential configuration.
import assert from 'node:assert/strict';
import {readFile, writeFile} from 'node:fs/promises';
import {createBrowserClient, SDKError} from '../out/tssdk/index.js';
const fixture = JSON.parse(await readFile(process.argv[2], 'utf8'));
const fetcher = globalThis.fetch;
globalThis.location = {origin: fixture.origin};
function browser(cookie) {
  globalThis.fetch = async (url, options) => {
    assert.equal(new URL(url).origin, fixture.origin, 'request left console origin');
    assert.equal(options.credentials, 'same-origin');
    assert.equal(options.redirect, 'error');
    assert.equal(options.headers.Authorization, undefined);
    assert.equal(options.headers.Cookie, undefined);
    const headers = new Headers(options.headers);
    headers.set('Cookie', cookie);
    headers.set('Sec-Fetch-Site', 'same-origin');
    if (options.method !== 'GET' && options.method !== 'HEAD') headers.set('Origin', fixture.origin);
    return fetcher(url, {...options, headers});
  };
  return createBrowserClient();
}
const owner = browser(fixture.owner);
const traceOptions = {traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01'};
const other = browser(fixture.other);
const created = await owner.createGateway({body: fixture.request}, traceOptions);
assert.equal(created.status, 201);
assert.equal(typeof created.body.id, 'string');
assert.equal(created.body.name, fixture.request.name);
const id = created.body.id;
assert.equal((await owner.getGateway({id})).body.id, id);
await assert.rejects(other.getGateway({id}), e => e instanceof SDKError && e.status === 404);
await assert.rejects(other.createGateway({body: {...fixture.request, name: 'sdk-denied'}}), e => e instanceof SDKError && e.status === 403);
const filtered = await other.listGateways({size: 10});
assert.equal(filtered.body.items.length, 0);
const renamed = await owner.updateGateway({id, body: {name: 'browser-sdk-renamed'}});
assert.equal(renamed.body.name, 'browser-sdk-renamed');
const visible = await owner.listGateways({size: 10});
assert.ok(visible.body.items.some(item => item.id === id));
await writeFile(process.argv[3], JSON.stringify({id}), {mode: 0o600});
