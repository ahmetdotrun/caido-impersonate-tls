import type { Profile } from "shared";

export const PROFILES: readonly Profile[] = [
  { id: "chrome_152", label: "Chrome 152", family: "chromium" },
  { id: "chrome_146", label: "Chrome 146", family: "chromium" },
  { id: "chrome_144", label: "Chrome 144", family: "chromium" },
  { id: "firefox_148", label: "Firefox 148", family: "firefox" },
  { id: "firefox_147", label: "Firefox 147", family: "firefox" },
  {
    id: "safari_ios_18_5",
    label: "Safari iOS 18.5",
    family: "mobile",
  },
  {
    id: "safari_ios_26_0",
    label: "Safari iOS 26.0",
    family: "mobile",
  },
  {
    id: "okhttp4_android_13",
    label: "OkHttp 4.10 / Android 13",
    family: "mobile",
  },
];

export function isKnownProfile(profile: string): boolean {
  return PROFILES.some((candidate) => candidate.id === profile);
}
