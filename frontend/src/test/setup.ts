import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => cleanup());

// A suíte roda em paralelo: telas com mais elementos podem passar de 1 s sob carga.
configure({ asyncUtilTimeout: 3000 });
