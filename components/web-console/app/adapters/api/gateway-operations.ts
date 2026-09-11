import type {
  GatewayControlPlane,
  GatewayFailureCode,
  GatewayFailureKind,
  GatewayInvocationContext,
  GatewayListRequest,
  GatewayPlacement,
  GatewayRecord,
  OpenShellGatewayServiceAccountCapabilities,
  OpenShellGatewayServiceAccountConnection,
  OpenShellGatewayServiceAccountCredential,
  OpenShellGatewayServiceAccountRecord,
} from "@openshift-online/hypershell-gateway-management-ui";
import {
  defaultGatewayListRequest,
  GatewayOperationError,
  normalizeGatewayPlacementClusterIds,
} from "@openshift-online/hypershell-gateway-management-ui";
import {
  SDKError,
  type Client,
  type RequestOptions,
  type Schemas,
} from "../../../../../out/tssdk/index.js";
type Gateway = Schemas["Gateway"];
type ManagedCluster = Schemas["ManagedCluster"];
type ApiServiceAccountCapabilities =
  Schemas["OpenShellGatewayServiceAccountCapabilities"];
type ApiServiceAccountConnection =
  Schemas["OpenShellGatewayServiceAccountConnection"];
type ApiServiceAccountCredential =
  Schemas["OpenShellGatewayServiceAccountCredential"];
type OpenShellGatewayServiceAccountCreateResponse =
  Schemas["OpenShellGatewayServiceAccountCreateResponse"];
type OpenShellGatewayServiceAccountGetResponse =
  Schemas["OpenShellGatewayServiceAccountGetResponse"];
type OpenShellGatewayServiceAccountListItem =
  Schemas["OpenShellGatewayServiceAccountListItem"];
type GatewayApiClient = Pick<
  Client,
  | "createGateway"
  | "deleteGateway"
  | "getGateway"
  | "listGateways"
  | "updateGateway"
  | "getManagedCluster"
  | "listManagedClusters"
  | "createGatewayServiceAccount"
  | "deleteGatewayServiceAccount"
  | "getGatewayServiceAccount"
  | "listGatewayServiceAccounts"
  | "revokeGatewayServiceAccount"
>;
type GatewayApiFactory = (correlationId: string) => GatewayApiClient;

function requirePage<T>(value: {
  page?: number;
  total?: number;
  items?: T[];
}): asserts value is { page: number; total: number; items: T[] } {
  if (
    !Number.isSafeInteger(value.page) ||
    (value.page ?? 0) < 1 ||
    !Number.isSafeInteger(value.total) ||
    (value.total ?? -1) < 0 ||
    !Array.isArray(value.items)
  )
    throw new GatewayOperationError("unavailable");
}

const placementPageSize = defaultGatewayListRequest.size;

const gatewaySortFields = {
  cluster: "cluster_id",
  created: "created_at",
  endpoint: "route_address",
  name: "name",
  owner: "created_by",
  status: "status",
} as const satisfies Record<GatewayListRequest["sortField"], string>;

function escapeIlikeLiteral(value: string): string {
  // Escape backslashes first so the escapes added for SQL wildcards remain
  // single escapes rather than being escaped again.
  return escapeSearchLiteral(value)
    .replaceAll("\\", "\\\\")
    .replaceAll("%", "\\%")
    .replaceAll("_", "\\_");
}

function escapeSearchLiteral(value: string): string {
  return value.replaceAll("'", "''");
}

function gatewaySearch(value: string): string | undefined {
  const query = value.trim();
  if (!query) {
    return undefined;
  }
  const literal = escapeIlikeLiteral(query);
  return ["name", "cluster_id", "status", "route_address", "external_dns"]
    .map((field) => `${field} ilike '%${literal}%'`)
    .join(" or ");
}

function apiClient(
  factory: GatewayApiFactory,
  context: GatewayInvocationContext,
): GatewayApiClient {
  return factory(context.correlationId);
}

function jsonObject(
  value: string | undefined,
): Record<string, unknown> | undefined {
  try {
    const parsed: unknown = JSON.parse(value ?? "");
    return typeof parsed === "object" &&
      parsed !== null &&
      !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : undefined;
  } catch {
    return undefined;
  }
}

function endpointFromRouteAddress(
  routeAddress: string | undefined,
): string | undefined {
  if (!routeAddress) {
    return undefined;
  }
  return routeAddress.replace(/^grpcs?:\/\//u, "");
}

function toGatewayRecord(gateway: Gateway): GatewayRecord {
  if (!gateway.id) throw new GatewayOperationError("unavailable");
  const oidc = jsonObject(gateway.oidc);
  const oidcAudience = optionalString(oidc?.audience);
  const oidcClientId = optionalString(oidc?.client_id);
  const oidcIssuer = optionalString(oidc?.issuer);
  const createdBy = optionalString(gateway.created_by);
  const activeSandboxCount = optionalNumber(gateway.active_sandbox_count);
  const consoleUrl = optionalString(gateway.console_address);

  return {
    ...(activeSandboxCount !== undefined ? { activeSandboxCount } : {}),
    clusterId: gateway.cluster_id,
    ...(consoleUrl ? { consoleUrl } : {}),
    ...(gateway.created_at ? { createdAt: gateway.created_at } : {}),
    ...(createdBy ? { createdBy } : {}),
    databaseId: gateway.database_id,
    externalDns:
      optionalString(gateway.external_dns) ||
      endpointFromRouteAddress(gateway.route_address),
    id: gateway.id,
    name: gateway.name,
    namespace: gateway.namespace,
    ...(oidcAudience ? { oidcAudience } : {}),
    ...(oidcClientId ? { oidcClientId } : {}),
    ...(oidcIssuer ? { oidcIssuer } : {}),
    phase: gateway.phase,
    releaseId: gateway.release_id,
    status: gateway.status,
  };
}

function optionalString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

type ApiServiceAccountRecord =
  | OpenShellGatewayServiceAccountCreateResponse
  | OpenShellGatewayServiceAccountGetResponse
  | OpenShellGatewayServiceAccountListItem;

function toServiceAccountRecord(
  account: ApiServiceAccountRecord,
): OpenShellGatewayServiceAccountRecord {
  const description = optionalString(account.description);
  const lastError = optionalString(account.last_error);
  const revokedAt = optionalString(account.revoked_at);
  return {
    clientId: account.client_id,
    createdAt: account.created_at,
    createdByUserId: account.created_by_user_id,
    ...(description ? { description } : {}),
    expiresAt: account.expires_at,
    gatewayId: account.gateway_id,
    id: account.id,
    ...(lastError ? { lastError } : {}),
    name: account.name,
    ...(revokedAt ? { revokedAt } : {}),
    role: account.role,
    status: account.status,
    subject: account.subject,
    updatedAt: account.updated_at,
  };
}

function toServiceAccountConnection(
  connection: ApiServiceAccountConnection,
): OpenShellGatewayServiceAccountConnection {
  return {
    accessTokenLifetimeSeconds: connection.access_token_lifetime_seconds,
    audience: connection.audience,
    clientId: connection.client_id,
    ...(connection.gateway_endpoint
      ? { gatewayEndpoint: connection.gateway_endpoint }
      : {}),
    gatewayName: connection.gateway_name,
    issuer: connection.issuer,
    tokenEndpoint: connection.token_endpoint,
  };
}

function toServiceAccountCredential(
  credential: ApiServiceAccountCredential,
): OpenShellGatewayServiceAccountCredential {
  return {
    ...toServiceAccountConnection(credential),
    clientSecret: credential.client_secret,
  };
}

function toServiceAccountCapabilities(
  capabilities: ApiServiceAccountCapabilities,
): OpenShellGatewayServiceAccountCapabilities {
  return {
    allowedRoles: capabilities.allowed_roles,
    canCreate: capabilities.can_create,
    canManageAll: capabilities.can_manage_all,
    expirationPolicy: {
      defaultSeconds: capabilities.expiration_policy.default_seconds,
      maximumSeconds: capabilities.expiration_policy.maximum_seconds,
      minimumSeconds: capabilities.expiration_policy.minimum_seconds,
    },
  };
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === "number" ? value : undefined;
}

function toGatewayPlacement(cluster: ManagedCluster): GatewayPlacement {
  if (!cluster.id) throw new GatewayOperationError("unavailable");
  const region = optionalString(cluster.region);
  const status = optionalString(cluster.status);
  return {
    id: cluster.id,
    name: cluster.name,
    provider: cluster.provider,
    ...(region ? { region } : {}),
    ...(status ? { status } : {}),
  };
}

function gatewayFailureKind(statusCode: number): GatewayFailureKind {
  if (statusCode === 401 || statusCode === 403) {
    return "denied";
  }
  if (statusCode === 404) {
    return "not-found";
  }
  if (statusCode === 409) {
    return "conflict";
  }
  if (statusCode === 408 || statusCode === 429 || statusCode >= 500) {
    return "unavailable";
  }
  return "unknown";
}

function gatewayFailureCode(code: string): GatewayFailureCode | undefined {
  if (code === "service_account_name_exists") {
    return "service-account-name-exists";
  }
  return undefined;
}

async function mapSDKFailure<T>(
  task: () => Promise<T>,
  onReauthRequired?: () => void,
): Promise<T> {
  try {
    return await task();
  } catch (error) {
    if (error instanceof SDKError) {
      if (error.code === "reauth_required") onReauthRequired?.();
      throw new GatewayOperationError(gatewayFailureKind(error.status), {
        cause: error,
        code: gatewayFailureCode(error.apiCode ?? ""),
      });
    }
    throw error;
  }
}

export function createGatewayControlPlaneAdapter(
  apiFactory: GatewayApiFactory,
  onReauthRequired?: () => void,
  traceParentFor?: (correlationId: string) => string | undefined,
): GatewayControlPlane {
  const requestOptions = (
    context: GatewayInvocationContext,
  ): RequestOptions => {
    const traceparent = traceParentFor?.(context.correlationId);
    return {
      signal: context.signal,
      ...(traceparent === undefined ? {} : { traceparent }),
    };
  };
  const mapFailure = <T>(task: () => Promise<T>) =>
    mapSDKFailure(task, onReauthRequired);
  return {
    async createOpenShellGatewayServiceAccount(gatewayId, input, context) {
      return mapFailure(async () => {
        const response = await apiClient(apiFactory, context)
          .createGatewayServiceAccount(
            {
              gateway_id: gatewayId,
              body: {
                ...(input.description === undefined
                  ? {}
                  : { description: input.description }),
                expires_at: input.expiresAt,
                name: input.name,
                role: input.role,
              },
            },
            requestOptions(context),
          )
          .then((result) => result.body);
        return {
          credential: toServiceAccountCredential(response.credential),
          serviceAccount: toServiceAccountRecord(response),
        };
      });
    },
    async deleteOpenShellGatewayServiceAccount(
      gatewayId,
      serviceAccountId,
      context,
    ) {
      await mapFailure(() =>
        apiClient(apiFactory, context)
          .deleteGatewayServiceAccount(
            { gateway_id: gatewayId, service_account_id: serviceAccountId },
            requestOptions(context),
          )
          .then((result) => result.body),
      );
    },
    async findGatewayPlacements(search, context) {
      return mapFailure(async () => {
        const normalizedSearch = search.trim();
        const literal = escapeIlikeLiteral(normalizedSearch);
        const result = await apiClient(apiFactory, context)
          .listManagedClusters(
            {
              ...{
                orderBy: "name asc",
                page: 1,
                ...(literal ? { search: `name ilike '%${literal}%'` } : {}),
                size: placementPageSize,
              },
            },
            requestOptions(context),
          )
          .then((result) => result.body);
        requirePage(result);
        const expectedItemCount = Math.min(placementPageSize, result.total);
        if (
          result.page !== 1 ||
          result.total < 0 ||
          result.items.length !== expectedItemCount
        ) {
          throw new GatewayOperationError("unavailable");
        }
        return {
          hasMore: result.total > result.items.length,
          items: result.items.map(toGatewayPlacement),
        };
      });
    },
    async getGatewayPlacement(clusterId, context) {
      return mapFailure(async () =>
        toGatewayPlacement(
          await apiClient(apiFactory, context)
            .getManagedCluster({ id: clusterId }, requestOptions(context))
            .then((result) => result.body),
        ),
      );
    },
    async getGatewayPlacements(clusterIds, context) {
      return mapFailure(async () => {
        const normalizedClusterIds =
          normalizeGatewayPlacementClusterIds(clusterIds);
        if (normalizedClusterIds.length === 0) {
          return [];
        }

        const requestedClusterIds = new Set(normalizedClusterIds);
        const result = await apiClient(apiFactory, context)
          .listManagedClusters(
            {
              ...{
                orderBy: "id asc",
                page: 1,
                search: `id in (${normalizedClusterIds
                  .map((clusterId) => `'${escapeSearchLiteral(clusterId)}'`)
                  .join(", ")})`,
                size: normalizedClusterIds.length,
              },
            },
            requestOptions(context),
          )
          .then((result) => result.body);
        requirePage(result);
        const returnedClusterIds = result.items
          .map(toGatewayPlacement)
          .map(({ id }) => id);
        if (
          result.page !== 1 ||
          result.total < 0 ||
          result.total > normalizedClusterIds.length ||
          result.items.length !== result.total ||
          new Set(returnedClusterIds).size !== returnedClusterIds.length ||
          returnedClusterIds.some(
            (clusterId) => !requestedClusterIds.has(clusterId),
          )
        ) {
          throw new GatewayOperationError("unavailable");
        }
        return result.items.map(toGatewayPlacement);
      });
    },
    async getGateway(gatewayId, context) {
      return mapFailure(async () =>
        toGatewayRecord(
          await apiClient(apiFactory, context)
            .getGateway({ id: gatewayId }, requestOptions(context))
            .then((result) => result.body),
        ),
      );
    },
    async getOpenShellGatewayServiceAccount(
      gatewayId,
      serviceAccountId,
      context,
    ) {
      return mapFailure(async () => {
        const response = await apiClient(apiFactory, context)
          .getGatewayServiceAccount(
            { gateway_id: gatewayId, service_account_id: serviceAccountId },
            requestOptions(context),
          )
          .then((result) => result.body);
        return {
          connection: toServiceAccountConnection(response.connection),
          serviceAccount: toServiceAccountRecord(response),
        };
      });
    },
    async listGateways(request, context) {
      return mapFailure(async () => {
        const search = gatewaySearch(request.search);
        const result = await apiClient(apiFactory, context)
          .listGateways(
            {
              ...{
                orderBy: `${gatewaySortFields[request.sortField]} ${request.sortDirection}`,
                page: request.page,
                ...(search === undefined ? {} : { search }),
                size: request.size,
              },
            },
            requestOptions(context),
          )
          .then((result) => result.body);
        requirePage(result);
        const pageOffset = (request.page - 1) * request.size;
        const expectedItemCount = Math.max(
          0,
          Math.min(request.size, result.total - pageOffset),
        );
        if (
          result.page !== request.page ||
          result.total < 0 ||
          result.items.length !== expectedItemCount
        ) {
          throw new GatewayOperationError("unavailable");
        }
        return {
          items: result.items.map(toGatewayRecord),
          page: result.page,
          size: request.size,
          total: result.total,
        };
      });
    },
    async listOpenShellGatewayServiceAccounts(gatewayId, request, context) {
      return mapFailure(async () => {
        const result = await apiClient(apiFactory, context)
          .listGatewayServiceAccounts(
            {
              gateway_id: gatewayId,
              ...{
                order: request.order,
                page: request.page,
                search: request.search,
                size: request.size,
                sort: request.sort,
                ...(request.status === undefined
                  ? {}
                  : { status: request.status }),
              },
            },
            requestOptions(context),
          )
          .then((result) => result.body);
        requirePage(result);
        const pageOffset = (request.page - 1) * request.size;
        const expectedItemCount = Math.max(
          0,
          Math.min(request.size, result.total - pageOffset),
        );
        if (
          result.page !== request.page ||
          result.total < 0 ||
          result.items.length !== expectedItemCount
        ) {
          throw new GatewayOperationError("unavailable");
        }
        return {
          capabilities: toServiceAccountCapabilities(result.capabilities),
          items: result.items.map(toServiceAccountRecord),
          page: result.page,
          size: result.size,
          total: result.total,
        };
      });
    },
    async provisionGateway(input, context) {
      return mapFailure(async () =>
        toGatewayRecord(
          await apiClient(apiFactory, context)
            .createGateway(
              {
                body: {
                  cluster_id: input.clusterId,
                  database_id: "",
                  name: input.name,
                  release_id: "",
                  route: JSON.stringify({ enabled: true }),
                },
              },
              requestOptions(context),
            )
            .then((result) => result.body),
        ),
      );
    },
    async removeGateway(gatewayId, context) {
      await mapFailure(() =>
        apiClient(apiFactory, context)
          .deleteGateway({ id: gatewayId }, requestOptions(context))
          .then((result) => result.body),
      );
    },
    async renameGateway(gatewayId, name, context) {
      return mapFailure(async () =>
        toGatewayRecord(
          await apiClient(apiFactory, context)
            .updateGateway(
              { id: gatewayId, body: { name } },
              requestOptions(context),
            )
            .then((result) => result.body),
        ),
      );
    },
    async revokeOpenShellGatewayServiceAccount(
      gatewayId,
      serviceAccountId,
      context,
    ) {
      return mapFailure(async () =>
        toServiceAccountRecord(
          await apiClient(apiFactory, context)
            .revokeGatewayServiceAccount(
              { gateway_id: gatewayId, service_account_id: serviceAccountId },
              requestOptions(context),
            )
            .then((result) => result.body),
        ),
      );
    },
  };
}
