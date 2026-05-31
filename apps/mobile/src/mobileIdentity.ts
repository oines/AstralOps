import * as Crypto from "expo-crypto";
import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import * as ed25519 from "@noble/ed25519";
import { sha256, sha512 } from "@noble/hashes/sha2.js";
import type { ControlCapability, DeviceIdentity } from "@astralops/protocol";

const IDENTITY_KEY = "astralops.mobile.identity.v1";
const IDENTITY_VERSION = 1;

(ed25519.hashes as { sha512?: (message: Uint8Array) => Uint8Array }).sha512 = sha512;

export const mobileControllerCapabilities: ControlCapability[] = [
  "core.read",
  "core.control",
  "interaction.respond",
  "session.edit",
  "attachment.ingest",
  "media.read",
  "media.download",
  "media.stream",
  "workspace.files.read",
  "workspace.files.write",
  "workspace.exec",
  "terminal.open",
  "terminal.input",
  "host.fs.browse",
  "host.manage",
];

export type StoredMobileIdentity = {
  version: typeof IDENTITY_VERSION;
  seed_hex: string;
  identity: DeviceIdentity;
};

export async function loadOrCreateMobileIdentity(): Promise<DeviceIdentity> {
  const existing = await loadStoredMobileIdentity();
  if (existing) return existing.identity;
  return createAndStoreMobileIdentity();
}

export async function resetMobileIdentity(): Promise<DeviceIdentity> {
  await SecureStore.deleteItemAsync(IDENTITY_KEY);
  return createAndStoreMobileIdentity();
}

export async function loadStoredMobileIdentity(): Promise<StoredMobileIdentity | undefined> {
  const raw = await SecureStore.getItemAsync(IDENTITY_KEY);
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw) as Partial<StoredMobileIdentity>;
    if (parsed.version !== IDENTITY_VERSION || typeof parsed.seed_hex !== "string" || !isDeviceIdentity(parsed.identity)) return undefined;
    return parsed as StoredMobileIdentity;
  } catch {
    return undefined;
  }
}

async function createAndStoreMobileIdentity(): Promise<DeviceIdentity> {
  const seed = Crypto.getRandomBytes(32);
  const publicKey = ed25519.getPublicKey(seed);
  const now = new Date().toISOString();
  const identity: DeviceIdentity = {
    device_id: `dev_${bytesToHex(Crypto.getRandomBytes(10))}`,
    device_name: defaultMobileDeviceName(),
    device_kind: "mobile",
    public_key: bytesToBase64(publicKey),
    public_key_fingerprint: publicKeyFingerprint(publicKey),
    capabilities: mobileControllerCapabilities,
    created_at: now,
    updated_at: now,
  };
  const stored: StoredMobileIdentity = {
    version: IDENTITY_VERSION,
    seed_hex: bytesToHex(seed),
    identity,
  };
  await SecureStore.setItemAsync(IDENTITY_KEY, JSON.stringify(stored), {
    keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK_THIS_DEVICE_ONLY,
  });
  return identity;
}

function defaultMobileDeviceName(): string {
  switch (Platform.OS) {
    case "ios":
      return "AstralOps iPhone";
    case "android":
      return "AstralOps Android";
    default:
      return "AstralOps Mobile";
  }
}

function publicKeyFingerprint(publicKey: Uint8Array): string {
  return `sha256:${bytesToHex(sha256(publicKey)).toUpperCase()}`;
}

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function bytesToBase64(bytes: Uint8Array): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  let output = "";
  for (let index = 0; index < bytes.length; index += 3) {
    const a = bytes[index];
    const b = bytes[index + 1] ?? 0;
    const c = bytes[index + 2] ?? 0;
    const triplet = (a << 16) | (b << 8) | c;
    output += alphabet[(triplet >> 18) & 63];
    output += alphabet[(triplet >> 12) & 63];
    output += index + 1 < bytes.length ? alphabet[(triplet >> 6) & 63] : "=";
    output += index + 2 < bytes.length ? alphabet[triplet & 63] : "=";
  }
  return output;
}

export function bytesToBase64URL(bytes: Uint8Array): string {
  return bytesToBase64(bytes).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function isDeviceIdentity(value: unknown): value is DeviceIdentity {
  if (!value || typeof value !== "object") return false;
  const record = value as Record<string, unknown>;
  return typeof record.device_id === "string"
    && typeof record.device_name === "string"
    && record.device_kind === "mobile"
    && typeof record.public_key === "string"
    && typeof record.public_key_fingerprint === "string"
    && Array.isArray(record.capabilities);
}
