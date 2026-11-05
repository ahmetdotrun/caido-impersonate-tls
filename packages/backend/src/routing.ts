import type { BackendSDK } from "./types";

type UpstreamRule = {
  id: string;
  enabled: boolean;
  // eslint-disable-next-line @typescript-eslint/no-restricted-types -- GraphQL uses null for absent relations.
  plugin: { id: string } | null;
};

// Caido 0.59 loads persisted rules before starting backend plugins. Reasserting
// enabled=true rebuilds their runtime binding without an off/on interval or
// writing back a stale copy of the user's filters and rank.
export async function refreshUpstreamRules(sdk: BackendSDK): Promise<void> {
  const pluginId = sdk.meta.id();
  const listed = await sdk.graphql.execute<{ upstreamPlugins: UpstreamRule[] }>(
    `query ImpersonateTLSUpstreamRules {
      upstreamPlugins { id enabled plugin { id } }
    }`,
  );
  if (
    (listed.errors?.length ?? 0) > 0 ||
    listed.data?.upstreamPlugins === undefined
  ) {
    throw new Error(
      `Could not read upstream rules: ${JSON.stringify(listed.errors ?? [])}`,
    );
  }

  for (const rule of listed.data.upstreamPlugins) {
    if (!rule.enabled || rule.plugin?.id !== pluginId) continue;

    const updated = await sdk.graphql.execute<{
      // eslint-disable-next-line @typescript-eslint/no-restricted-types -- GraphQL uses null for failed mutations.
      toggleUpstreamPlugin: { upstream: UpstreamRule | null };
    }>(
      `mutation ImpersonateTLSRefreshUpstreamRule($id: ID!) {
        toggleUpstreamPlugin(id: $id, enabled: true) {
          upstream { id enabled plugin { id } }
        }
      }`,
      { id: rule.id },
    );
    const rebound = updated.data?.toggleUpstreamPlugin?.upstream;
    if (
      (updated.errors?.length ?? 0) > 0 ||
      rebound?.id !== rule.id ||
      rebound.enabled !== true ||
      rebound.plugin?.id !== pluginId
    ) {
      throw new Error(
        `Could not refresh upstream rule ${rule.id}: ${JSON.stringify(updated.errors ?? [])}`,
      );
    }
  }
}
