import { mount } from "svelte";
import "./app.css";
import Panel from "./Panel.svelte";
import Main from "./Main.svelte";

// One bundle, two windows: Tauri opens index.html#panel and
// index.html#main; the screenshot script opens the same URLs.
const hash = location.hash.replace(/^#/, "");
const [view, query] = hash.split("?");
const params = new URLSearchParams(query ?? "");
document.body.classList.add(view === "panel" ? "win-panel" : "win-main");

mount(view === "panel" ? Panel : Main, {
  target: document.getElementById("app")!,
  props: { startPage: params.get("page") },
});
