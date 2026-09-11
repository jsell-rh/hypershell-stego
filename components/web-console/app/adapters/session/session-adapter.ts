import {
  createBrowserClient,
  type Client,
} from "../../../../../out/tssdk/index.js";

export interface BrowserSessionUser {
  email?: string;
  name?: string;
  preferredUsername?: string;
  sub?: string;
}
export interface BrowserSession {
  authenticated: boolean;
  expiresAt?: number;
  roles: string[];
  user?: BrowserSessionUser;
}
export interface SessionGateway {
  getSession(signal?: AbortSignal): Promise<BrowserSession>;
}
export function createSessionAdapter(
  client?: Pick<Client, "session">,
): SessionGateway {
  return {
    async getSession(signal) {
      const value = await (client ?? createBrowserClient()).session(
        signal ? { signal } : undefined,
      );
      return {
        authenticated: value.authenticated,
        roles: value.roles,
        ...(value.expires_at === undefined
          ? {}
          : { expiresAt: value.expires_at }),
        ...(value.user
          ? {
              user: {
                email: value.user.email,
                name: value.user.name,
                preferredUsername: value.user.preferred_username,
                sub: value.user.sub,
              },
            }
          : {}),
      };
    },
  };
}
