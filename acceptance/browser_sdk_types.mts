import {createBrowserClient} from '../out/tssdk/index.js';
const client = createBrowserClient();
const created = await client.createGateway({body: {name: 'browser', cluster_id: 'cluster', release_id: 'release', database_id: ''}});
await client.getGateway({id: 'gateway'});
await client.listGateways({size: 10});
await client.updateGateway({id: 'gateway', body: {name: 'renamed'}});
// @ts-expect-error The Gateway ID is required.
client.getGateway({});
// @ts-expect-error The API requires a numeric page size.
client.listGateways({size: '10'});
// @ts-expect-error OAuth credentials are not browser request options.
client.getGateway({id: 'gateway'}, {token: 'private'});
void created;
