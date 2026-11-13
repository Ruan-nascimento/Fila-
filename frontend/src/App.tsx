import { Link, NavLink, Route, Routes } from "react-router";
import { ApiStatus } from "./features/status/ApiStatus";
import { Cadastro, EsqueciSenha, ReenviarVerificacao } from "./pages/Contas";
import { Conta } from "./pages/Conta";
import { Entrar } from "./pages/Entrar";
import { GestaoInicio, GestaoLoja } from "./pages/gestao/Gestao";
import { AceitarConvite, RedefinirSenha, VerificarEmail } from "./pages/Links";
import { Loja } from "./pages/Loja";
import { useSession } from "./session/context";

// Etapa 02: acesso (conta, sessão, convite e visitante). Fila, comanda e pagamento chegam nas etapas 03–11.
export function App() {
  return (
    <div className="layout">
      <a className="skip" href="#conteudo">
        Pular para o conteúdo
      </a>
      <header className="layout__header">
        <Link to="/" className="brand">
          Fila
        </Link>
        <Menu />
      </header>
      <main id="conteudo" className="layout__main">
        <Routes>
          <Route path="/" element={<Inicio />} />
          <Route path="/entrar" element={<Entrar />} />
          <Route path="/cadastro" element={<Cadastro />} />
          <Route path="/reenviar-verificacao" element={<ReenviarVerificacao />} />
          <Route path="/verificar-email" element={<VerificarEmail />} />
          <Route path="/esqueci-senha" element={<EsqueciSenha />} />
          <Route path="/redefinir-senha" element={<RedefinirSenha />} />
          <Route path="/convite" element={<AceitarConvite />} />
          <Route path="/conta" element={<Conta />} />
          <Route path="/loja/:slug" element={<Loja />} />
          <Route path="/gestao" element={<GestaoInicio />} />
          <Route path="/gestao/:estId/*" element={<GestaoLoja />} />
          <Route path="*" element={<NaoEncontrada />} />
        </Routes>
      </main>
    </div>
  );
}

function Menu() {
  const { state } = useSession();
  const logged = state.status === "ready" && state.session.account !== null;
  const staff = state.status === "ready" && state.session.session?.kind === "staff";
  return (
    <nav aria-label="Principal">
      <ul className="menu">
        {staff && (
          <li>
            <NavLink to="/gestao">Gestão</NavLink>
          </li>
        )}
        {logged ? (
          <li>
            <NavLink to="/conta">Minha conta</NavLink>
          </li>
        ) : (
          <>
            <li>
              <NavLink to="/entrar">Entrar</NavLink>
            </li>
            <li>
              <NavLink to="/cadastro">Criar conta</NavLink>
            </li>
          </>
        )}
      </ul>
    </nav>
  );
}

function Inicio() {
  return (
    <section aria-labelledby="titulo-inicio">
      <h1 id="titulo-inicio">Fila da barbearia</h1>
      <p>
        Clientes chegam pelo link ou QR code da loja (endereço <code>/loja/nome-da-loja</code>). A equipe
        entra com a conta convidada pelo dono.
      </p>
      <ApiStatus />
    </section>
  );
}

function NaoEncontrada() {
  return (
    <section aria-labelledby="titulo-404">
      <h2 id="titulo-404">Página não encontrada</h2>
      <p>
        <Link to="/">Voltar ao início</Link>
      </p>
    </section>
  );
}
