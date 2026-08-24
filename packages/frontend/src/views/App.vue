<script setup lang="ts">
import Button from "primevue/button";
import Card from "primevue/card";
import Divider from "primevue/divider";
import Message from "primevue/message";
import Select from "primevue/select";
import Tab from "primevue/tab";
import TabList from "primevue/tablist";
import TabPanel from "primevue/tabpanel";
import TabPanels from "primevue/tabpanels";
import Tabs from "primevue/tabs";
import Tag from "primevue/tag";
import ToggleSwitch from "primevue/toggleswitch";
import type { Profile, Result, Settings, TransportStatus } from "shared";
import { computed, onMounted, ref } from "vue";

import { useSDK } from "../plugins/sdk";

import ActivityPanel from "./ActivityPanel.vue";

const sdk = useSDK();

const profiles = ref<Profile[]>([]);
const settings = ref<Settings>();
const status = ref<TransportStatus>();
const enabled = ref(false);
const autoStart = ref(false);
const defaultProfile = ref<string>();
const loading = ref(true);
const saving = ref(false);
const action = ref<"start" | "stop">();
const error = ref("");

function unwrap<T>(result: Result<T>): T {
  if (result.kind === "Error") {
    throw new Error(result.error);
  }
  return result.value;
}

function applySettings(value: Settings): void {
  settings.value = value;
  enabled.value = value.enabled;
  autoStart.value = value.autoStart;
  defaultProfile.value = value.defaultProfile;
}

function applyStatus(value: TransportStatus): void {
  status.value = value;
}

const profileOptions = computed(() =>
  profiles.value.map((profile) => ({
    label: profile.label,
    value: profile.id,
  })),
);

const statusSeverity = computed(() => {
  switch (status.value?.state) {
    case "running":
      return "success";
    case "error":
    case "unsupported":
      return "danger";
    case "starting":
    case "stopping":
      return "warn";
    default:
      return "secondary";
  }
});

const statusDetail = computed(() => {
  if (status.value === undefined) {
    return "Loading transport status";
  }
  return `${status.value.platform} · transport ${status.value.version}`;
});

const canStart = computed(
  () =>
    action.value === undefined &&
    status.value?.state !== "running" &&
    status.value?.state !== "starting",
);

const canStop = computed(
  () =>
    action.value === undefined &&
    status.value?.state !== "idle" &&
    status.value?.state !== "stopping",
);

async function load(): Promise<void> {
  loading.value = true;
  error.value = "";
  try {
    const [statusResult, settingsResult, profilesResult] = await Promise.all([
      sdk.backend.getStatus(),
      sdk.backend.getSettings(),
      sdk.backend.getProfiles(),
    ]);
    profiles.value = unwrap(profilesResult);
    applySettings(unwrap(settingsResult));
    applyStatus(unwrap(statusResult));
  } catch (cause) {
    error.value = String(cause);
  } finally {
    loading.value = false;
  }
}

async function startTransport(): Promise<void> {
  action.value = "start";
  error.value = "";
  try {
    applyStatus(unwrap(await sdk.backend.startTransport()));
  } catch (cause) {
    error.value = String(cause);
  } finally {
    action.value = undefined;
  }
}

async function stopTransport(): Promise<void> {
  action.value = "stop";
  error.value = "";
  try {
    applyStatus(unwrap(await sdk.backend.stopTransport()));
  } catch (cause) {
    error.value = String(cause);
  } finally {
    action.value = undefined;
  }
}

async function save(): Promise<void> {
  if (settings.value === undefined || defaultProfile.value === undefined) {
    return;
  }

  saving.value = true;
  error.value = "";
  try {
    const updated = unwrap(
      await sdk.backend.updateSettings({
        ...settings.value,
        enabled: enabled.value,
        autoStart: autoStart.value,
        defaultProfile: defaultProfile.value,
        headerMode: "preserve",
      }),
    );
    applySettings(updated);
    sdk.window.showToast("Impersonate TLS settings saved", {
      variant: "success",
    });
  } catch (cause) {
    error.value = String(cause);
  } finally {
    saving.value = false;
  }
}

sdk.backend.onEvent("transport:status", applyStatus);
sdk.backend.onEvent("settings:updated", applySettings);
onMounted(() => void load());
</script>

<template>
  <div class="h-full min-h-0 flex flex-col gap-1">
    <Card
      class="shrink-0"
      :pt="{
        body: { class: 'p-0' },
        content: { class: 'p-0' },
      }"
    >
      <template #content>
        <div class="px-4 py-3 flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h3 class="text-lg font-semibold">Impersonate TLS</h3>
            <p class="text-sm text-surface-400">
              Browser-like TLS and HTTP profiles for Caido upstream rules
            </p>
          </div>
          <Tag
            :value="status?.state ?? 'loading'"
            :severity="statusSeverity"
            class="capitalize shrink-0"
          />
        </div>
      </template>
    </Card>

    <Card
      class="flex-1 min-h-0"
      :pt="{
        root: {
          style: 'display: flex; flex-direction: column; height: 100%;',
        },
        body: { class: 'flex-1 p-0 flex flex-col min-h-0' },
        content: { class: 'flex-1 flex flex-col min-h-0' },
      }"
    >
      <template #content>
        <Tabs value="settings" class="flex-1 min-h-0 flex flex-col">
          <TabList>
            <Tab value="settings">Settings</Tab>
            <Tab value="activity">Activity</Tab>
          </TabList>
          <TabPanels class="flex-1 min-h-0 p-0 overflow-hidden">
            <TabPanel value="settings" class="h-full min-h-0">
              <div class="h-full min-h-0 overflow-auto p-4">
                <div class="flex flex-col gap-5">
                  <Message
                    v-if="error !== ''"
                    severity="error"
                    :closable="false"
                  >
                    {{ error }}
                  </Message>

                  <div class="flex items-center gap-4">
                    <div class="flex-1 min-w-0">
                      <div class="text-sm font-medium">Transport</div>
                      <p class="text-sm text-surface-400">{{ statusDetail }}</p>
                      <p
                        v-if="status?.error !== undefined"
                        class="text-sm text-red-400"
                      >
                        {{ status.error }}
                      </p>
                    </div>
                    <div class="flex items-center gap-2 shrink-0">
                      <Button
                        label="Start"
                        icon="fas fa-play"
                        size="small"
                        :disabled="!canStart"
                        :loading="action === 'start'"
                        @click="startTransport"
                      />
                      <Button
                        label="Stop"
                        icon="fas fa-stop"
                        severity="secondary"
                        outlined
                        size="small"
                        :disabled="!canStop"
                        :loading="action === 'stop'"
                        @click="stopTransport"
                      />
                    </div>
                  </div>

                  <Divider />

                  <div class="flex items-center gap-4">
                    <div class="flex-1 min-w-0">
                      <label class="text-sm font-medium" for="enabled-toggle">
                        Enable shaping
                      </label>
                      <p class="text-sm text-surface-400">
                        Apply the selected profile to matching Upstream Plugin
                        rules
                      </p>
                    </div>
                    <div class="w-56 shrink-0 flex justify-end">
                      <ToggleSwitch
                        v-model="enabled"
                        input-id="enabled-toggle"
                        :disabled="loading"
                      />
                    </div>
                  </div>

                  <div class="flex items-center gap-4">
                    <div class="flex-1 min-w-0">
                      <label class="text-sm font-medium" for="autostart-toggle">
                        Start with plugin
                      </label>
                      <p class="text-sm text-surface-400">
                        Launch the packaged transport when Caido loads the
                        plugin
                      </p>
                    </div>
                    <div class="w-56 shrink-0 flex justify-end">
                      <ToggleSwitch
                        v-model="autoStart"
                        input-id="autostart-toggle"
                        :disabled="loading"
                      />
                    </div>
                  </div>

                  <div class="flex items-center gap-4">
                    <div class="flex-1 min-w-0">
                      <label class="text-sm font-medium" for="profile-select">
                        Transport profile
                      </label>
                      <p class="text-sm text-surface-400">
                        TLS ClientHello, ALPN, and HTTP/2 identity
                      </p>
                    </div>
                    <div class="w-56 shrink-0">
                      <Select
                        v-model="defaultProfile"
                        input-id="profile-select"
                        :options="profileOptions"
                        option-label="label"
                        option-value="value"
                        :disabled="loading"
                        class="w-full"
                      />
                    </div>
                  </div>

                  <Divider />

                  <Message severity="secondary" :closable="false">
                    Select Impersonate TLS for a domain under Settings →
                    Upstream Plugins. No separate HTTP or SOCKS proxy is
                    required.
                  </Message>

                  <div class="flex justify-end">
                    <Button
                      label="Save changes"
                      icon="fas fa-floppy-disk"
                      :loading="saving"
                      :disabled="
                        loading ||
                        saving ||
                        settings === undefined ||
                        defaultProfile === undefined
                      "
                      @click="save"
                    />
                  </div>
                </div>
              </div>
            </TabPanel>
            <TabPanel value="activity" class="h-full min-h-0">
              <ActivityPanel />
            </TabPanel>
          </TabPanels>
        </Tabs>
      </template>
    </Card>
  </div>
</template>
