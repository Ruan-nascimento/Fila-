import { ApiStatus } from "./features/status/ApiStatus";

// Etapa 01: só a estrutura. As jornadas de cliente, profissional e dono chegam nas etapas 02–11.
export function App() {
  return (
    <div className="layout">
      <header className="layout__header">
        <h1>Fila</h1>
      </header>
      <main className="layout__main">
        <p>Ambiente de desenvolvimento. As telas do sistema chegam nas próximas etapas.</p>
        <ApiStatus />
      </main>
    </div>
  );
}
