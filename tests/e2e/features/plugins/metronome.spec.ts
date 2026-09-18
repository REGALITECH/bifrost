import { test, expect } from "../../core/fixtures/base.fixture";
type TestConfig = {
  api_key: string;
  dry_run: boolean;
  model_mapping?: Record<string, string>;
};
type UpdatePluginRequest = { enabled: boolean; config?: TestConfig; replace_config?: boolean };

test("builtin Metronome can be saved, enabled and disabled without custom-plugin controls", async ({
  page,
}, testInfo) => {
  let plugin = {
    name: "metronome",
    enabled: false,
    loaded: false,
    isCustom: false,
    config: { api_key: "env.METRONOME_API_KEY", dry_run: true } as TestConfig,
    status: { name: "metronome", status: "disabled", logs: [] as string[], types: [] as string[] },
  };
  let missingKey = false;
  const updates: UpdatePluginRequest[] = [];
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/plugins/metronome" && route.request().method() === "PUT") {
      const data = route.request().postDataJSON() as UpdatePluginRequest;
      updates.push(data);
      plugin = {
        ...plugin,
        enabled: data.enabled,
        loaded: data.enabled && !missingKey,
        config: data.config ?? plugin.config,
        status: {
          name: "metronome",
          status: data.enabled ? (missingKey ? "error" : "active") : "disabled",
          logs:
            missingKey && data.enabled
              ? ["METRONOME_API_KEY is unset or empty in the Bifrost process"]
              : [],
          types: ["llm"],
        },
      };
      if (missingKey && data.enabled) {
        await route.fulfill({ status: 500, json: { error: { message: plugin.status!.logs[0] } } });
      } else {
        await route.fulfill({ json: { message: "Plugin updated successfully", plugin } });
      }
      return;
    }
    const body =
      path === "/api/plugins"
        ? { plugins: [plugin], count: 1 }
        : path === "/api/plugins/metronome"
          ? plugin
          : path === "/api/config"
            ? {
                is_db_connected: true,
                client_config: {},
                governance_config: {},
                framework_config: {},
                metadata: { onboarding_dismissed: true },
              }
            : path === "/api/session/is-auth-enabled"
              ? { is_auth_enabled: false, has_valid_token: false }
              : path === "/api/version"
                ? "test"
                : {};
    await route.fulfill({ json: body });
  });
  await page.route("https://getbifrost.ai/**", (route) => route.fulfill({ json: {} }));
  await page.goto("/workspace/plugins");
  await expect(page.getByTestId("plugin-list-item").filter({ hasText: "metronome" })).toBeVisible();
  await expect(page.getByLabel("Path", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Delete Plugin" })).toHaveCount(0);
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("disabled");
  await page.locator(".monaco-editor .view-lines").click();
  // Monaco follows the desktop browser's platform keymap.
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Backspace");
  await expect(page.locator(".monaco-editor .view-lines")).not.toContainText("api_key");
  await page.keyboard.type(
    JSON.stringify({
      api_key: "env.METRONOME_API_KEY",
      dry_run: false,
      model_mapping: { "openai/test": "billing-model" },
    }),
  );
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
  expect(updates[0].config!.api_key).toBe("env.METRONOME_API_KEY");
  expect(updates[0].replace_config).toBe(true);
  expect(updates[0].config!.dry_run).toBe(false);
  expect(updates[0].config!.model_mapping).toEqual({ "openai/test": "billing-model" });
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("disabled");

  missingKey = true;
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("error");
  await expect(page.getByTestId("plugin-save-error")).toContainText("METRONOME_API_KEY");
  await expect(page.getByTestId("plugin-logs")).toContainText("unset or empty");
  // The desired state was persisted, but loading failed. A retry must remain
  // possible even when there are no further edits after the failed save.
  await expect(page.getByTestId("plugin-save-button")).toBeEnabled();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("No instance loaded");
  await page.screenshot({ path: testInfo.outputPath("metronome-error.png"), fullPage: true });
  missingKey = false;
  for (const toast of await page.getByRole("button", { name: "Close toast" }).all())
    await toast.click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
  await expect(page.getByTestId("plugin-save-error")).toHaveCount(0);
  await page.reload();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
});