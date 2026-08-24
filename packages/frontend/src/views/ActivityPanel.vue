<script setup lang="ts">
import Button from "primevue/button";
import Column from "primevue/column";
import DataTable from "primevue/datatable";
import Message from "primevue/message";
import Tag from "primevue/tag";
import type { ActivityEntry, ActivityState, Result } from "shared";
import { onMounted, ref } from "vue";

import { useSDK } from "../plugins/sdk";

const sdk = useSDK();
const entries = ref<ActivityEntry[]>([]);
const loading = ref(true);
const clearing = ref(false);
const error = ref("");

function unwrap<T>(result: Result<T>): T {
  if (result.kind === "Error") {
    throw new Error(result.error);
  }
  return result.value;
}

function upsert(entry: ActivityEntry): void {
  const index = entries.value.findIndex(
    (candidate) => candidate.id === entry.id,
  );
  if (index === -1) {
    entries.value.unshift(entry);
  } else {
    entries.value[index] = entry;
  }
}

async function refresh(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    entries.value = unwrap(await sdk.backend.getActivity());
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function clear(): Promise<void> {
  clearing.value = true;
  error.value = "";
  try {
    unwrap(await sdk.backend.clearActivity());
  } catch (cause) {
    error.value = String(cause);
  } finally {
    clearing.value = false;
  }
}

function stateSeverity(state: ActivityState): "danger" | "info" | "success" {
  switch (state) {
    case "succeeded":
      return "success";
    case "failed":
      return "danger";
    default:
      return "info";
  }
}

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString();
}

function formatTarget(entry: ActivityEntry): string {
  return `${entry.scheme}://${entry.host}:${entry.port.toString()}`;
}

function formatResult(entry: ActivityEntry): string {
  switch (entry.state) {
    case "succeeded":
      return `${entry.statusCode?.toString() ?? "response"} · ${entry.protocol ?? "HTTP"}`;
    case "failed":
      return entry.error ?? "Transport failed";
    default:
      return "Waiting for the transport";
  }
}

sdk.backend.onEvent("activity:updated", upsert);
sdk.backend.onEvent("activity:cleared", () => {
  entries.value = [];
});
onMounted(() => void refresh());
</script>

<template>
  <div class="h-full min-h-0 flex flex-col gap-4 p-4">
    <div class="flex items-center justify-between gap-4">
      <div class="min-w-0">
        <h3 class="font-semibold">Transport activity</h3>
        <p class="text-sm text-surface-400">
          Live evidence that requests reached the private transport and target
        </p>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <Button
          label="Refresh"
          icon="fas fa-rotate"
          severity="secondary"
          outlined
          size="small"
          :loading="loading"
          @click="refresh"
        />
        <Button
          label="Clear"
          icon="fas fa-trash"
          severity="secondary"
          text
          size="small"
          :disabled="entries.length === 0"
          :loading="clearing"
          @click="clear"
        />
      </div>
    </div>

    <Message v-if="error !== ''" severity="error" :closable="false">
      {{ error }}
    </Message>

    <Message severity="secondary" :closable="false">
      Kept in memory only, up to 250 entries. Paths, queries, headers, bodies,
      cookies, and transport tokens are never recorded.
    </Message>

    <div class="flex-1 min-h-0">
      <DataTable
        :value="entries"
        data-key="id"
        scrollable
        scroll-height="flex"
        size="small"
        striped-rows
        :loading="loading"
        class="h-full"
      >
        <template #empty>
          <div class="p-4 text-center text-sm text-surface-400">
            No activity yet. Send a request that matches the Impersonate TLS
            upstream rule.
          </div>
        </template>

        <Column header="Time" class="whitespace-nowrap">
          <template #body="{ data }">
            {{ formatTime(data.startedAt) }}
          </template>
        </Column>
        <Column header="State" class="whitespace-nowrap">
          <template #body="{ data }">
            <Tag
              :value="data.state"
              :severity="stateSeverity(data.state)"
              class="capitalize"
            />
          </template>
        </Column>
        <Column field="method" header="Method" class="whitespace-nowrap" />
        <Column header="Target">
          <template #body="{ data }">
            <span class="font-mono text-sm">{{ formatTarget(data) }}</span>
          </template>
        </Column>
        <Column field="profile" header="Profile" class="whitespace-nowrap" />
        <Column header="Result">
          <template #body="{ data }">
            <span :title="formatResult(data)">{{ formatResult(data) }}</span>
          </template>
        </Column>
        <Column header="Duration" class="whitespace-nowrap">
          <template #body="{ data }">
            {{ data.durationMs === undefined ? "—" : `${data.durationMs} ms` }}
          </template>
        </Column>
      </DataTable>
    </div>
  </div>
</template>
