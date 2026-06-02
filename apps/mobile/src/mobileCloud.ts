import * as Crypto from "expo-crypto";
import * as Linking from "expo-linking";
import * as WebBrowser from "expo-web-browser";
import type { CloudAuthProvider } from "@astralops/protocol";
import { CloudHttpClient } from "@astralops/controller-client";

export const DEFAULT_CLOUD_BASE_URL = "https://cloud-astralops.oines.dev";

export type MobileCloudLoginCode = {
  base_url: string;
  login_code: string;
};

WebBrowser.maybeCompleteAuthSession();

export async function requestCloudLoginCode(provider: CloudAuthProvider, baseUrl = DEFAULT_CLOUD_BASE_URL): Promise<MobileCloudLoginCode> {
  const normalizedBaseURL = normalizeBaseUrl(baseUrl);
  const state = bytesToBase64URL(Crypto.getRandomBytes(24));
  const redirectUri = Linking.createURL("cloud-auth/callback");
  const client = new CloudHttpClient({ baseUrl: normalizedBaseURL });
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
  return {
    base_url: normalizedBaseURL,
    login_code: loginCode,
  };
}

function stringParam(value: string | string[] | undefined): string {
  return Array.isArray(value) ? value[0] ?? "" : value ?? "";
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/+$/g, "") || DEFAULT_CLOUD_BASE_URL;
}

function bytesToBase64URL(bytes: Uint8Array): string {
  return bytesToBase64(bytes).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function bytesToBase64(bytes: Uint8Array): string {
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
