import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "./ui/App";
import "./styles.css";
import { demoBigSum } from "./generated/demobig";

const queryClient = new QueryClient();

// Demo build knob: importing this module makes TS/Vite work scale with generated size.
if ((import.meta as any).env?.DEMO_BIG === "1") {
  // eslint-disable-next-line no-console
  console.log("demo big sum", demoBigSum());
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </React.StrictMode>,
);

