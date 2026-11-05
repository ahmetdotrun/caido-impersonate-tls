import type { ActivityEntry } from "./index";

export const MAX_ACTIVITY_ENTRIES = 250;

export function mergeActivity(
  entries: ActivityEntry[],
  updates: ActivityEntry[],
): ActivityEntry[] {
  const merged = new Map(entries.map((entry) => [entry.id, entry]));
  for (const entry of updates) {
    const previous = merged.get(entry.id);
    if (
      previous?.completedAt !== undefined &&
      (entry.completedAt === undefined ||
        entry.completedAt < previous.completedAt)
    )
      continue;
    merged.set(entry.id, entry);
  }
  return [...merged.values()]
    .sort((a, b) => b.startedAt - a.startedAt || b.sequence - a.sequence)
    .slice(0, MAX_ACTIVITY_ENTRIES);
}
