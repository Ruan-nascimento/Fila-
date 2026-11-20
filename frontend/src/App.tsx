import { lazy, Suspense, type ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router";
import { Compass } from "lucide-react";
import { Cadastro, EsqueciSenha, ReenviarVerificacao } from "./pages/Contas";
import { Conta, ExcluirContaPagina } from "./pages/Conta";
import { DashboardSoNoPrograma } from "./pages/dono/Dashboard";
import { AcessoDono } from "./pages/dono/Dono";
import { modoDono } from "./pages/dono/modo";
import { Entrar } from "./pages/Entrar";
import { ConsultarExclusao } from "./pages/exclusao/Exclusao";
import { AceitarConvite, RedefinirSenha, VerificarEmail } from "./pages/Links";
import { PainelCliente } from "./pages/cliente/Painel";
import { Loja } from "./pages/Loja";
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
          <PublicShell width="wide">
            <Inicio />
          </PublicShell>
        }
      />
      <Route path="/entrar" element={auth(<Entrar />)} />
      {/* Entrada do dono, aberta pelo programa Fila Gestão (mesmo login e mesma gestão). */}
      <Route
        path="/dono"
        element={
          <PublicShell width="narrow">
            <AcessoDono />
          </PublicShell>
        }
      />
      <Route
        path="/dono/painel-analitico"
        element={
          <PublicShell width="narrow">
            <DashboardSoNoPrograma />
          </PublicShell>
        }
      />
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
  if (modoDono()) return <Navigate to="/dono" replace />;
  return <PainelCliente />;
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
