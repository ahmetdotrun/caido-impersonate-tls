import { PROFILES } from "../../shared/src/catalog";

export { PROFILES };

export function isKnownProfile(profile: string): boolean {
  return PROFILES.some((candidate) => candidate.id === profile);
}
