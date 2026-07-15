import { expect, test } from "@playwright/test";

let seededNoteId = 0;
let seededTitle = "";
let seededToken = "";

test.beforeAll(async ({ request }) => {
  const username = `e2e_seed_${Date.now()}`;
  const registration = await request.post("/api/v1/auth/register", {
    data: { username, password: "e2e_password_123", nickname: "E2E Seed" },
  });
  expect(registration.ok()).toBeTruthy();
  const auth = await registration.json();
  seededToken = auth.access_token;
  seededTitle = `E2E 检索与详情 ${Date.now()}`;
  const created = await request.post("/api/v1/notes", {
    headers: { Authorization: `Bearer ${seededToken}` },
    data: {
      title: seededTitle,
      body: "这是一条由 Playwright 创建的端到端测试笔记，包含完整正文和可检索限制条件。",
      category: "study",
      tags: ["e2e", "retrieval"],
      media: [{
        media_type: "image",
        url: "",
        caption: "E2E 结构化媒体",
        ocr_text: "Playwright OCR 证据文本",
        position: 1,
      }],
    },
  });
  expect(created.ok()).toBeTruthy();
  seededNoteId = (await created.json()).id;
});

test.afterAll(async ({ request }) => {
  if (seededNoteId && seededToken) {
    await request.delete(`/api/v1/notes/${seededNoteId}`, {
      headers: { Authorization: `Bearer ${seededToken}` },
    });
  }
});

test("search, deep link, ranking, and runtime status use the live platform", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByPlaceholder("搜索全部笔记的标题与正文").fill(seededTitle);
  await page.getByRole("button", { name: `打开笔记：${seededTitle}` }).click();
  await expect(page).toHaveURL(new RegExp(`/notes/${seededNoteId}$`));
  const detail = page.getByRole("dialog", { name: "笔记详情" });
  await expect(detail.getByRole("heading", { name: seededTitle })).toBeVisible();
  await expect(detail.getByText("Playwright OCR 证据文本")).toBeVisible();
  await detail.getByRole("button", { name: "关闭详情" }).click();

  if (testInfo.project.name === "mobile-chromium") {
    await page.getByRole("navigation", { name: "移动端导航" }).getByRole("button", { name: "热榜", exact: true }).click();
  } else {
    await page.getByRole("button", { name: "今日热榜", exact: true }).click();
  }
  await expect(page.getByRole("heading", { name: "今日热榜" })).toBeVisible();
  if (testInfo.project.name === "mobile-chromium") {
    await page.getByRole("navigation", { name: "移动端导航" }).getByRole("button", { name: "状态", exact: true }).click();
  } else {
    await page.getByRole("button", { name: "系统状态", exact: true }).click();
  }
  await expect(page.getByRole("heading", { name: "系统运行状态" })).toBeVisible();
  const availability = page.getByRole("region", { name: "服务可用性" });
  await expect(availability.getByText("Go API")).toBeVisible();
  await expect(availability.getByText("Event Worker")).toBeVisible();
  await expect(availability.getByText("NATS JetStream")).toBeVisible();
  await expect(availability.getByText("ready")).toHaveCount(3);
});

test("a user can register, publish OCR text, comment, like, and collect", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "mobile-chromium", "full authoring workflow is covered on desktop; mobile navigation is covered separately");
  const username = `e2e_ui_${Date.now()}`;
  const title = `E2E UI 闭环 ${Date.now()}`;
  await page.goto("/");
  await page.getByRole("button", { name: "登录" }).click();
  const authDialog = page.locator(".auth-dialog");
  await authDialog.getByRole("button", { name: "注册" }).click();
  await authDialog.getByLabel("用户名").fill(username);
  await authDialog.getByLabel("昵称").fill("E2E 用户");
  await authDialog.getByLabel("密码").fill("e2e_password_123");
  await authDialog.getByRole("button", { name: "注册并登录" }).click();
  await expect(page.getByRole("button", { name: new RegExp(username) })).toBeVisible();

  await page.getByRole("button", { name: "发布笔记", exact: true }).first().click();
  const compose = page.getByRole("dialog", { name: "发布一篇图文笔记" });
  await compose.getByLabel("标题").fill(title);
  await compose.getByLabel("正文").fill("Playwright 验证登录态、作者身份、正文以及异步互动链路。");
  await compose.getByLabel("媒体 OCR / caption 文字").fill("E2E UI OCR 文本资产");
  await compose.getByRole("button", { name: "发布笔记" }).click();

  const detail = page.getByRole("dialog", { name: "笔记详情" });
  await expect(detail.getByRole("heading", { name: title })).toBeVisible();
  await detail.getByPlaceholder("写下你的验证结果或疑问").fill("E2E 评论链路通过");
  await detail.getByRole("button", { name: "发布", exact: true }).click();
  await expect(detail.getByText("E2E 评论链路通过")).toBeVisible();
  await detail.getByRole("button", { name: "点赞笔记" }).click();
  await expect(page.getByRole("status")).toContainText("已点赞");
  await detail.getByRole("button", { name: "收藏笔记" }).click();
  await expect(page.getByRole("status")).toContainText("已收藏");
});
