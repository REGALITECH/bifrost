import { test, expect } from "../../core/fixtures/base.fixture";
type TestConfig = {
  api_key: { ref: string; type: string; value: string };
  dry_run: boolean;
};
type UpdatePluginRequest = { enabled: boolean; config?: TestConfig };

test("builtin Metronome can be saved, enabled and disabled without custom-plugin controls", async ({
  page,
}) => {
  const apiKey = { ref: "env.METRONOME_API_KEY", type: "env", value: "<REDACTED>" };
  let plugin = {
    name: "metronome",
    enabled: false,
    isCustom: false,
    path: null,
    config: { api_key: apiKey, dry_run: true } as TestConfig,
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
        config: { ...plugin.config, ...data.config },
        status: {
          name: "metronome",
          status: data.enabled ? (missingKey ? "error" : "active") : "disabled",
          logs:
            missingKey && data.enabled
              ? ["metronome api_key is required for live delivery"]
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
  await expect(page.getByLabel("Path", { exact: true })).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Delete Plugin" })).toHaveCount(0);
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("disabled");
  await page.locator(".monaco-editor .view-lines").click();
  // Monaco follows the desktop browser's platform keymap.
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Backspace");
  await expect(page.locator(".monaco-editor .view-lines")).not.toContainText("api_key");
  await page.keyboard.type(
    JSON.stringify({
      api_key: apiKey,
      dry_run: false,
    }),
  );
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
  expect(updates[0].config!.api_key).toEqual(apiKey);
  expect(updates[0].config!.dry_run).toBe(false);
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("disabled");

  missingKey = true;
  await page.getByTestId("plugin-enabled-switch").click();
  await page.getByTestId("plugin-save-button").click();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("error");
  await expect(page.getByTestId("plugin-logs")).toContainText("api_key is required for live delivery");
  // The desired state was persisted, but loading failed. A retry must remain
  // possible even when there are no further edits after the failed save.
  await expect(page.getByTestId("plugin-save-button")).toBeEnabled();
  missingKey = false;
  await page.getByTestId("plugin-save-button").press("Enter");
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
  await page.reload();
  await expect(page.getByTestId("plugin-runtime-status")).toContainText("active");
});
