import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import { App } from "./App";
import { AppearanceSync } from "./features/theme/AppearanceSync";
import { SessionProvider } from "./session/session";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("Elemento #root não encontrado em index.html");

createRoot(root).render(
  <StrictMode>
    <BrowserRouter>
      <SessionProvider>
        <AppearanceSync />
        <App />
      </SessionProvider>
    </BrowserRouter>
  </StrictMode>,
);
