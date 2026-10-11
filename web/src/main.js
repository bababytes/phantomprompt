import { createApp } from "vue";
import App from "./App.vue";
import { access, showLocked } from "./config.js";
import "./style.css";

if (access) createApp(App).mount("#app");
else showLocked();
