package atendimento

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fila/internal/auditoria"
	"fila/internal/catalogo"
	"fila/internal/estabelecimento"
	"fila/internal/platform/database"
	hs "fila/internal/platform/httpserver"
	"fila/internal/platform/validate"
)

// Itens com quantidade (ADR-0033). Uma linha por serviço (ou produto) com quantidade; o
// servidor calcula preço, duração e pontos a partir da cópia (snapshot) do catálogo.
//
//	subtotal = quantidade × preço unitário registrado
//	duração  = quantidade × duração unitária registrada (só serviços)
//	pontos   = unidades cobradas × pontos unitários registrados (só serviços)
//
// Aumentar a quantidade de uma linha existente mantém as condições registradas para ela; uma
// linha nova copia o catálogo vigente. Mudanças são por QUANTIDADE ABSOLUTA com versão esperada
// da comanda: repetir o comando não soma de novo.

// Limites de formato (o limite da loja, menor, vem das configurações).
const (
	maxServiceLines = 10
	maxProductLines = 10
	maxLineQuantity = estabelecimento.MaxServiceQuantity
)

// LineRequest é um item pedido: exatamente um de service_id/product_id, com quantidade.
type LineRequest struct {
	ServiceID *uuid.UUID `json:"service_id"`
	ProductID *uuid.UUID `json:"product_id"`
	Quantity  *int64     `json:"quantity"`
}

// lineKey identifica a linha pelo item do catálogo.
type lineKey struct {
	Kind string
	ID   uuid.UUID
}

// LineWant é um item pedido já validado no formato.
type LineWant struct {
	Key      lineKey
	Quantity int
	field    string
}

// Line é um item resolvido no catálogo, pronto para gravar.
type Line struct {
	Snap     catalogo.Snapshot
	Quantity int
}

// ServiceLines converte a lista antiga (service_ids, quantidade 1 cada) para linhas.
func ServiceLines(ids []uuid.UUID, field string) []LineWant {
	out := make([]LineWant, len(ids))
	for i, id := range ids {
		out[i] = LineWant{Key: lineKey{catalogo.KindService, id}, Quantity: 1, field: field + "/" + strconv.Itoa(i)}
	}
	return out
}

// ParseLines confere o formato: um id por linha, quantidade inteira de 1 a 50, sem linha
// repetida, até 10 serviços e 10 produtos. minServices exige serviços (entrada na fila: nunca só
// produto).
func ParseLines(v *validate.Collector, field string, lines *[]LineRequest, minServices int) []LineWant {
	if lines == nil {
		v.Required(field, minServices > 0)
		return nil
	}
	var out []LineWant
	seen := map[lineKey]bool{}
	services, products := 0, 0
	for i, l := range *lines {
		f := field + "/" + strconv.Itoa(i)
		var key lineKey
		switch {
		case l.ServiceID != nil && l.ProductID != nil:
			v.Add(&hs.FieldError{Field: f, Code: hs.FieldNotAllowed, Message: "informe service_id OU product_id"})
			continue
		case l.ServiceID != nil:
			key = lineKey{catalogo.KindService, *l.ServiceID}
			services++
		case l.ProductID != nil:
			key = lineKey{catalogo.KindProduct, *l.ProductID}
			products++
		default:
			v.Add(&hs.FieldError{Field: f + "/service_id", Code: hs.FieldRequired, Message: "informe o serviço ou o produto"})
			continue
		}
		q := int64(1)
		if l.Quantity != nil {
			q = *l.Quantity
			v.IntRange(f+"/quantity", l.Quantity, 1, maxLineQuantity)
		}
		if seen[key] {
			v.Add(&hs.FieldError{Field: f, Code: "duplicate", Message: "item repetido: ajuste a quantidade na mesma linha"})
		}
		seen[key] = true
		out = append(out, LineWant{Key: key, Quantity: int(q), field: f})
	}
	switch {
	case services < minServices:
		v.Add(&hs.FieldError{Field: field, Code: hs.FieldTooShort, Message: "escolha pelo menos um serviço"})
	case services > maxServiceLines || products > maxProductLines:
		v.Add(&hs.FieldError{Field: field, Code: hs.FieldTooLong, Message: "no máximo 10 serviços e 10 produtos diferentes"})
	}
	return out
}

// snapshotsFor lê do catálogo (FOR SHARE) os serviços e produtos citados.
func snapshotsFor(ctx context.Context, q database.Querier, estID uuid.UUID, keys []lineKey) (map[lineKey]catalogo.Snapshot, error) {
	var sIDs, pIDs []uuid.UUID
	for _, k := range keys {
		if k.Kind == catalogo.KindService {
			sIDs = append(sIDs, k.ID)
		} else {
			pIDs = append(pIDs, k.ID)
		}
	}
	out := map[lineKey]catalogo.Snapshot{}
	if len(sIDs) > 0 {
		m, err := catalogo.ServicesForEntry(ctx, q, estID, sIDs)
		if err != nil {
			return nil, err
		}
		for id, sn := range m {
			out[lineKey{catalogo.KindService, id}] = sn
		}
	}
	if len(pIDs) > 0 {
		m, err := catalogo.ProductsForEntry(ctx, q, estID, pIDs)
		if err != nil {
			return nil, err
		}
		for id, sn := range m {
			out[lineKey{catalogo.KindProduct, id}] = sn
		}
	}
	return out, nil
}

// checkGrowth confere uma linha nova ou uma quantidade MAIOR: item desta loja, ativo, feito pelo
// profissional (serviço), produtos ligados (produto) e quantidade dentro do limite da loja. O
// limite não vale retroativamente: manter (ou diminuir) uma quantidade já aceita é sempre possível.
func checkGrowth(sn catalogo.Snapshot, found bool, w LineWant, current int, professionalID uuid.UUID, professionalName string,
	st estabelecimento.Settings) error {
	kindName := "serviço"
	if w.Key.Kind == catalogo.KindProduct {
		kindName = "produto"
	}
	switch {
	case !found:
		return hs.ErrValidation(hs.FieldError{Field: w.field, Code: hs.FieldNotAllowed, Message: kindName + " não encontrado nesta loja"})
	case w.Key.Kind == catalogo.KindProduct && !st.ProductsEnabled:
		return conflict("products_disabled", "Produtos indisponíveis",
			"Esta barbearia não está oferecendo produtos no momento.", nil)
	case !sn.Active:
		code := "service_inactive"
		if w.Key.Kind == catalogo.KindProduct {
			code = "product_inactive"
		}
		return conflict(code, "Item indisponível", fmt.Sprintf("O %s %q não está disponível no momento.", kindName, sn.Name), nil)
	case !sn.Enabled(professionalID):
		who := professionalName
		if who == "" {
			who = "O profissional deste atendimento"
		}
		return conflict("service_not_eligible", "Serviço não feito por este profissional",
			fmt.Sprintf("%s não faz o serviço %q. Escolha outro profissional ou serviço.", who, sn.Name), nil)
	case w.Quantity > st.MaxServiceQuantity && w.Quantity > current:
		return hs.ErrValidation(hs.FieldError{Field: w.field + "/quantity", Code: "quantity_limit",
			Message: fmt.Sprintf("no máximo %d unidades do mesmo item por pedido", st.MaxServiceQuantity)})
	}
	return nil
}

// ResolveNewLines resolve as linhas de um pedido NOVO (entrada na fila): todas são inclusões.
func ResolveNewLines(ctx context.Context, q database.Querier, estID, professionalID uuid.UUID, professionalName string,
	wants []LineWant, st estabelecimento.Settings) ([]Line, error) {
	keys := make([]lineKey, len(wants))
	for i, w := range wants {
		keys[i] = w.Key
	}
	snaps, err := snapshotsFor(ctx, q, estID, keys)
	if err != nil {
		return nil, err
	}
	out := make([]Line, 0, len(wants))
	for _, w := range wants {
		sn, ok := snaps[w.Key]
		if err := checkGrowth(sn, ok, w, 0, professionalID, professionalName, st); err != nil {
			return nil, err
		}
		out = append(out, Line{Snap: sn, Quantity: w.Quantity})
	}
	return out, nil
}

// qtyEvent é uma mudança de quantidade para o histórico (order_item_quantity_change).
type qtyEvent struct {
	ItemID   uuid.UUID
	Field    string
	From, To int
}

// recordQuantities grava o histórico das quantidades com a versão da comanda depois da mudança.
func recordQuantities(ctx context.Context, q database.Querier, estID, orderID uuid.UUID, evs []qtyEvent, source string,
	a auditoria.Event, version int64, now time.Time) error {
	var account *uuid.UUID
	switch a.ActorKind {
	case auditoria.ActorCustomer, auditoria.ActorProfessional, auditoria.ActorOwner:
		account = a.ActorID
	}
	kind := a.ActorKind
	if kind == "" || kind == auditoria.ActorOperator {
		kind = auditoria.ActorSystem
	}
	for _, e := range evs {
		if e.From == e.To {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO order_item_quantity_change (establishment_id, order_id, item_id, field, from_value, to_value, source,
			                                        actor_kind, actor_account_id, order_version, at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			estID, orderID, e.ItemID, e.Field, e.From, e.To, source, kind, account, version, now); err != nil {
			return fmt.Errorf("histórico de quantidades: %w", err)
		}
	}
	return nil
}

// planItems compara o conjunto desejado (quantidade absoluta por item do catálogo) com as linhas
// atuais e aplica: retira (só sem unidade realizada), muda a quantidade (nunca abaixo do já
// realizado) e inclui linhas novas com o catálogo vigente. Devolve os eventos de quantidade e se
// algo mudou. Não sobe a versão: quem chama recalcula o total (bumpOpenTotal) e grava o histórico.
func planItems(ctx context.Context, tx pgx.Tx, o orderRow, items []itemRow, wants []LineWant, professionalName string,
	st estabelecimento.Settings, now time.Time) (evs []qtyEvent, changed bool, err error) {
	byKey := map[lineKey]itemRow{}
	for _, it := range items {
		byKey[it.key()] = it
	}
	want := map[lineKey]LineWant{}
	for _, w := range wants {
		want[w.Key] = w
	}
	// Retiradas e reduções primeiro (sem unidade realizada apagada).
	for _, it := range items {
		w, keep := want[it.key()]
		switch {
		case !keep && it.PerformedQty > 0:
			return nil, false, itemPerformedConflict(it, 0)
		case !keep:
			if _, err := tx.Exec(ctx, `UPDATE order_item SET status = 'removed', removed_at = $2 WHERE id = $1`, it.ID, now); err != nil {
				return nil, false, err
			}
			evs, changed = append(evs, qtyEvent{it.ID, "quantity", it.Quantity, 0}), true
		case w.Quantity < it.PerformedQty:
			return nil, false, itemPerformedConflict(it, w.Quantity)
		}
	}
	// Linhas que crescem ou são novas precisam do catálogo atual.
	var growKeys []lineKey
	for _, w := range wants {
		if it, ok := byKey[w.Key]; !ok || w.Quantity > it.Quantity {
			growKeys = append(growKeys, w.Key)
		}
	}
	snaps, err := snapshotsFor(ctx, tx, o.EstablishmentID, growKeys)
	if err != nil {
		return nil, false, err
	}
	for _, w := range wants {
		it, exists := byKey[w.Key]
		if exists && w.Quantity == it.Quantity {
			continue
		}
		if !exists || w.Quantity > it.Quantity {
			sn, ok := snaps[w.Key]
			cur := 0
			if exists {
				cur = it.Quantity
			}
			if err := checkGrowth(sn, ok, w, cur, o.ProfessionalID, professionalName, st); err != nil {
				return nil, false, err
			}
			if !exists {
				id, err := insertItem(ctx, tx, o.EstablishmentID, o.ID, sn, w.Quantity, now)
				if err != nil {
					return nil, false, err
				}
				_ = id
				changed = true
				continue
			}
		}
		if err := setQuantity(ctx, tx, it, w.Quantity, now); err != nil {
			return nil, false, err
		}
		evs, changed = append(evs, qtyEvent{it.ID, "quantity", it.Quantity, w.Quantity}), true
	}
	return evs, changed, nil
}

// setQuantity muda a quantidade pedida de uma linha aberta, mantendo a situação coerente: com
// todas as unidades feitas a linha fica "realizada"; com unidade faltando, volta a "aceita".
func setQuantity(ctx context.Context, q database.Querier, it itemRow, to int, now time.Time) error {
	_, err := q.Exec(ctx, `
		UPDATE order_item SET quantity = $2,
		       status = CASE WHEN performed_quantity >= $2 THEN 'performed' ELSE 'accepted' END,
		       performed_at = CASE WHEN performed_quantity >= $2 THEN coalesce(performed_at, $3) ELSE NULL END
		WHERE id = $1`, it.ID, to, now)
	return err
}

func itemPerformedConflict(it itemRow, to int) error {
	unit := "realizada"
	if it.Kind == catalogo.KindProduct {
		unit = "entregue"
	}
	detail := fmt.Sprintf("%q já tem %d unidade(s) %s e não pode ser retirado.", it.Name, it.PerformedQty, unit)
	if to > 0 {
		detail = fmt.Sprintf("%q já tem %d unidade(s) %s: a quantidade não pode ficar abaixo disso.", it.Name, it.PerformedQty, unit)
	}
	return conflict("item_already_performed", "Unidades já realizadas", detail, nil)
}
