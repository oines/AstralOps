import * as Crypto from "expo-crypto";
import * as Linking from "expo-linking";
import * as SecureStore from "expo-secure-store";
import * as WebBrowser from "expo-web-browser";
import type { CloudAccount, CloudAccountStatus, CloudAuthProvider, CloudDeviceRecord, CloudMembershipLease, CloudRelayListResponse, DeviceIdentity, RemoteHostRecord } from "@astralops/protocol";
import { CloudHttpClient } from "@astralops/controller-client";
import { bytesToBase64URL, mobileControllerCapabilities } from "./mobileIdentity";

export const DEFAULT_CLOUD_BASE_URL = "https://cloud-astralops.oines.dev";

const CLOUD_SESSION_KEY = "astralops.mobile.cloud-session.v1";
const CLOUD_SESSION_VERSION = 1;

WebBrowser.maybeCompleteAuthSession();

export type StoredCloudSession = {
  version: typeof CLOUD_SESSION_VERSION;
  base_url: string;
  account_token: string;
  account_id_hash?: string;
  relay_id?: string;
  relay_url?: string;
  relay_credential?: string;
  membership_signing_public_key?: string;
  membership_lease?: CloudMembershipLease;
  expires_at?: string;
  updated_at: string;
};

export type MobileHostRecord = RemoteHostRecord & {
  public_key: string;
};

export type CloudMeshSnapshot = {
  session: StoredCloudSession;
  account: CloudAccountStatus;
  relays: CloudRelayListResponse;
  devices: CloudDeviceRecord[];
  hosts: MobileHostRecord[];
};

export async function loadStoredCloudSession(): Promise<StoredCloudSession | undefined> {
  const raw = await SecureStore.getItemAsync(CLOUD_SESSION_KEY);
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw) as Partial<StoredCloudSession>;
    if (parsed.version !== CLOUD_SESSION_VERSION || typeof parsed.base_url !== "string" || typeof parsed.account_token !== "string") return undefined;
    return parsed as StoredCloudSession;
  } catch {
    return undefined;
  }
}

export async function saveCloudSession(session: StoredCloudSession): Promise<void> {
  await SecureStore.setItemAsync(CLOUD_SESSION_KEY, JSON.stringify(session), {
    keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY,
  });
}

export async function clearStoredCloudSession(): Promise<void> {
  await SecureStore.deleteItemAsync(CLOUD_SESSION_KEY);
}

export async function startCloudOAuth(provider: CloudAuthProvider, identity: DeviceIdentity, baseUrl = DEFAULT_CLOUD_BASE_URL): Promise<StoredCloudSession> {
  const state = bytesToBase64URL(Crypto.getRandomBytes(24));
  const redirectUri = Linking.createURL("cloud-auth/callback");
  const client = new CloudHttpClient({ baseUrl });
  const authUrl = client.authStartUrl(provider, redirectUri, state);
  const result = await WebBrowser.openAuthSessionAsync(authUrl, redirectUri);
  if (result.type !== "success") {
    throw new Error("Cloud sign-in was cancelled.");
  }
  const callback = Linking.parse(result.url);
  const returnedState = stringParam(callback.queryParams?.state);
  const loginCode = stringParam(callback.queryParams?.login_code);
  const error = stringParam(callback.queryParams?.error);
  if (error) throw new Error(error);
  if (!loginCode) throw new Error("Cloud sign-in did not return a login code.");
  if (returnedState !== state) throw new Error("Cloud sign-in state mismatch.");

  const exchanged = await client.exchangeLoginCode(loginCode, identity);
  const relay = exchanged.account.relay;
  const session: StoredCloudSession = {
    version: CLOUD_SESSION_VERSION,
    base_url: normalizeBaseUrl(baseUrl),
    account_token: exchanged.account_token,
    account_id_hash: exchanged.account.account_id_hash,
    relay_id: relay?.relay_id,
    relay_url: relay?.relay_url,
    relay_credential: relay?.credential,
    membership_signing_public_key: exchanged.account.membership_signing_public_key,
    membership_lease: exchanged.device?.membership_lease,
    expires_at: exchanged.expires_at,
    updated_at: new Date().toISOString(),
  };
  await saveCloudSession(session);
  return session;
}

export async function loadCloudMeshSnapshot(session: StoredCloudSession, identity: DeviceIdentity): Promise<CloudMeshSnapshot> {
  const client = cloudClient(session);
  const account = await client.account();
  const accountStatus = cloudAccountStatus(account);
  const relayUrl = account.relay?.relay_url ?? session.relay_url ?? "";
  const heartbeat = await client.heartbeat(identity.device_id, relayUrl);
  const [relays, devices] = await Promise.all([
    client.relays().catch(() => ({ relays: [], current_relay_id: account.relay?.relay_id })),
    client.devices(),
  ]);
  const nextSession = mergeSessionCloudFacts(session, account, heartbeat);
  await saveCloudSession(nextSession);
  return {
    session: nextSession,
    account: accountStatus,
    relays,
    devices,
    hosts: cloudDevicesToRemoteHosts(devices, identity.device_id, accountStatus),
  };
}

export async function requestCloudPairing(session: StoredCloudSession, identity: DeviceIdentity, hostDeviceId: string): Promise<void> {
  await cloudClient(session).requestPairing({
    host_device_id: hostDeviceId,
    controller_device_id: identity.device_id,
    scope: "full",
    capabilities: mobileControllerCapabilities,
    workspace_exec_policy: "trusted",
  });
}

export async function removeSelfFromCloud(session: StoredCloudSession, identity: DeviceIdentity): Promise<void> {
  await cloudClient(session).removeDevice(identity.device_id);
}

function cloudClient(session: StoredCloudSession): CloudHttpClient {
  return new CloudHttpClient({ baseUrl: session.base_url, accountToken: session.account_token });
}

function mergeSessionCloudFacts(session: StoredCloudSession, account: CloudAccount, selfDevice?: CloudDeviceRecord): StoredCloudSession {
  return {
    ...session,
    account_id_hash: account.account_id_hash,
    relay_id: account.relay?.relay_id,
    relay_url: account.relay?.relay_url,
    relay_credential: account.relay?.credential ?? session.relay_credential,
    membership_signing_public_key: account.membership_signing_public_key ?? session.membership_signing_public_key,
    membership_lease: selfDevice?.membership_lease ?? session.membership_lease,
    updated_at: new Date().toISOString(),
  };
}

function cloudDevicesToRemoteHosts(devices: CloudDeviceRecord[], selfDeviceId: string, account: CloudAccountStatus): MobileHostRecord[] {
  return devices
    .filter((device) => device.device_id !== selfDeviceId && device.can_host && device.status !== "revoked")
    .map((device) => {
      const online = device.status === "online";
      const relayURL = device.relay_url || account.relay?.relay_url || "";
      const connection = online && relayURL ? "relay" : "offline";
      const authorization = online ? "needs_pairing" : "known";
      return {
        device_id: device.device_id,
        device_name: device.device_name,
        device_kind: device.device_kind,
        public_key: device.public_key,
        public_key_fingerprint: device.public_key_fingerprint,
        known_identity: true,
        status: online ? "online" : "offline",
        connection,
        authorization_state: authorization,
        capabilities: device.capabilities,
        control: {
          state: online ? "needs_pairing" : "idle",
          transport: connection === "relay" ? "relay" : undefined,
          route_generation: 0,
          updated_at: device.updated_at,
        },
      } satisfies MobileHostRecord;
    });
}

function cloudAccountStatus(account: CloudAccount): CloudAccountStatus {
  return {
    account_id_hash: account.account_id_hash,
    relay: account.relay ? {
      relay_id: account.relay.relay_id,
      relay_url: account.relay.relay_url,
      region: account.relay.region,
      name: account.relay.name,
      credential_available: Boolean(account.relay.credential),
      credential_expires_at: account.relay.credential_expires_at,
    } : undefined,
  };
}

function stringParam(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/+$/g, "") || DEFAULT_CLOUD_BASE_URL;
}
