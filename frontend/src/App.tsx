import { lazy, Suspense, type ReactNode } from "react";
import { Route, Routes } from "react-router";
import { Compass, QrCode, Store, UsersRound } from "lucide-react";
import { ApiStatus } from "./features/status/ApiStatus";
import { Cadastro, EsqueciSenha, ReenviarVerificacao } from "./pages/Contas";
import { Conta, ExcluirContaPagina } from "./pages/Conta";
import { Entrar } from "./pages/Entrar";
import { ConsultarExclusao } from "./pages/exclusao/Exclusao";
import { AceitarConvite, RedefinirSenha, VerificarEmail } from "./pages/Links";
import { Loja } from "./pages/Loja";
import { useSession } from "./session/context";
import { AuthShell, PublicShell } from "./ui/layout";
import { ButtonLink, EmptyState, Loading } from "./ui/ui";

// A área da equipe vem em um arquivo separado: quem abre a loja no celular não baixa a gestão.
const GestaoInicio = lazy(() => import("./pages/gestao/Gestao").then((m) => ({ default: m.GestaoInicio })));
const GestaoLoja = lazy(() => import("./pages/gestao/Gestao").then((m) => ({ default: m.GestaoLoja })));

// Jornadas de visitante, cliente, profissional e dono, do celular ao computador. Cada grupo de
// rotas tem sua moldura: pública/cliente, autenticação, loja (com o nome no cabeçalho) e equipe.
export function App() {
  const auth = (el: ReactNode) => <AuthShell>{el}</AuthShell>;
  return (
    <Routes>
      <Route
        path="/"
        element={
          <PublicShell width="narrow">
            <Inicio />
          </PublicShell>
        }
      />
      <Route path="/entrar" element={auth(<Entrar />)} />
      <Route path="/cadastro" element={auth(<Cadastro />)} />
      <Route path="/reenviar-verificacao" element={auth(<ReenviarVerificacao />)} />
      <Route path="/verificar-email" element={auth(<VerificarEmail />)} />
      <Route path="/esqueci-senha" element={auth(<EsqueciSenha />)} />
      <Route path="/redefinir-senha" element={auth(<RedefinirSenha />)} />
      <Route path="/convite" element={auth(<AceitarConvite />)} />
      <Route path="/exclusao" element={auth(<ConsultarExclusao />)} />
      <Route
        path="/conta"
        element={
          <PublicShell>
            <Conta />
          </PublicShell>
        }
      />
      <Route
        path="/conta/excluir"
        element={
          <PublicShell width="narrow">
            <ExcluirContaPagina />
          </PublicShell>
        }
      />
      <Route path="/loja/:slug/*" element={<Loja />} />
      <Route
        path="/gestao"
        element={
          <PublicShell>
            <Suspense fallback={<Loading />}>
              <GestaoInicio />
            </Suspense>
          </PublicShell>
        }
      />
      <Route
        path="/gestao/:estId/*"
        element={
          <Suspense fallback={<Loading>Carregando gestão…</Loading>}>
            <GestaoLoja />
          </Suspense>
        }
      />
      <Route
        path="*"
        element={
          <PublicShell>
            <NaoEncontrada />
          </PublicShell>
        }
      />
    </Routes>
  );
}

function Inicio() {
  const { state } = useSession();
  const staff = state.status === "ready" && state.session.session?.kind === "staff";
  const account = state.status === "ready" ? state.session.account : null;
  return (
    <section aria-labelledby="titulo-inicio" className="grid gap-6 pt-4 text-center sm:pt-10">
      <span className="mx-auto inline-flex size-16 items-center justify-center rounded-2xl bg-select text-accent">
        <QrCode aria-hidden="true" className="size-8" />
      </span>
      <div className="grid gap-2">
        <h1 id="titulo-inicio" className="m-0 text-2xl font-bold tracking-tight sm:text-3xl">
          Abra o link ou o QR code da sua barbearia
        </h1>
        <p className="mx-auto m-0 max-w-md text-fg-muted">
          O link da loja mostra as filas, a espera estimada e os serviços. Você entra na fila com um apelido
          ou com a sua conta.
        </p>
      </div>
      <div className="mx-auto grid w-full max-w-sm gap-3">
        {staff ? (
          <ButtonLink to="/gestao" icon={Store} block>
            Ir para a gestão
          </ButtonLink>
        ) : account ? (
          <ButtonLink to="/conta" icon={UsersRound} block>
            Minha conta
          </ButtonLink>
        ) : (
          <>
            <ButtonLink to="/entrar" icon={UsersRound} block>
              Entrar na minha conta
            </ButtonLink>
            <ButtonLink to="/entrar?como=equipe" variant="secondary" icon={Store} block>
              Acesso da equipe
            </ButtonLink>
          </>
        )}
      </div>
      <p className="m-0 text-sm text-fg-muted">A equipe entra com a conta convidada pelo dono da loja.</p>
      <ApiStatus quietWhenReady />
    </section>
  );
}

function NaoEncontrada() {
  return (
    <EmptyState
      icon={Compass}
      title={<span className="text-xl">Página não encontrada</span>}
      action={<ButtonLink to="/">Voltar ao início</ButtonLink>}
    >
      <p>Confira o endereço. Se veio de um link da loja, abra-o de novo.</p>
    </EmptyState>
  );
}
