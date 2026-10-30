package whatsapp

import (
	"errors"
	"time"
)

// Debit est ce que la passerelle annonce de son propre bridage.
//
// IL VIT À LA RACINE ET NON DANS LE PAQUET DU FOURNISSEUR, pour la raison qui vaut déjà pour
// [ErrQuotaAtteint] : un appelant qui doit décider combien de temps attendre n'aurait sinon
// aucun moyen de lire cette durée sans importer AyosPush, c'est à dire sans apprendre le nom
// du fournisseur pour écrire une attente. Chaque passerelle annonce son bridage dans ses
// propres en-têtes, et seul son adaptateur sait les traduire ; ce que l'appelant lit est
// cette valeur-ci, qui ne nomme personne.
//
// LA VALEUR ZÉRO VEUT DIRE « LA PASSERELLE N'A RIEN ANNONCÉ », et c'est le cas normal : rien
// n'oblige une passerelle à documenter son bridage, et ce module ne devine ni ne calcule
// aucune de ces deux valeurs. Un appelant qui ne trouve rien ici garde la décision, avec son
// propre délai de repli.
type Debit struct {
	// Limite est le nombre d'envois par fenêtre que la passerelle dit accepter.
	//
	// Elle sert à CONSTATER UNE DÉRIVE, et non à calculer quoi que ce soit. Un plafond
	// configuré chez l'appelant qui s'écarte de celui-ci annonce un refus à venir, et
	// l'écart devient visible avant le premier 429 plutôt qu'après. Zéro quand la
	// passerelle n'annonce rien.
	Limite int

	// Reprise est le délai avant qu'un envoi redevienne possible.
	//
	// C'est la seule valeur sur laquelle il y ait une décision à prendre, et elle a
	// l'avantage de venir de la passerelle : attendre ce qu'elle demande vaut mieux que
	// deviner, et c'est la seule façon de ne pas rejouer trop tôt un envoi qu'elle vient de
	// refuser. Zéro quand elle n'annonce rien.
	Reprise time.Duration
}

// Annonce dit si la passerelle a annoncé quoi que ce soit de son bridage.
func (d Debit) Annonce() bool { return d.Limite > 0 || d.Reprise > 0 }

// QuotaError signale une saturation et porte ce que la passerelle en a dit.
//
// ELLE EMBALLE LA CAUSE AU LIEU DE LA REMPLACER, et c'est ce qui la rend sûre à introduire :
// [ErrQuotaAtteint] et le motif du fournisseur restent tous deux interrogeables par errors.Is
// sur la même erreur, donc aucun appelant écrit avant elle ne change. Ce qu'elle ajoute se lit
// par errors.As, ou par [DelaiAvantRejeu] qui évite d'écrire le errors.As.
//
// SON MESSAGE N'EMPILE PAS LA CAUSE. Ce dépôt refuse les messages empilés, et un « quota
// depasse: quota d envoi atteint » dans chaque journal serait exactement cela. Le délai
// annoncé, lui, s'ajoute : c'est un fait de plus et non une répétition, et c'est le premier
// que l'exploitant cherche quand il lit un refus.
type QuotaError struct {
	// Debit est ce que la passerelle a annoncé. Sa valeur zéro dit qu'elle n'a rien annoncé.
	Debit Debit

	// Err est la cause, telle que l'adaptateur de la passerelle l'a formulée.
	Err error
}

// Error rend le message de la cause, suivi du délai annoncé quand il y en a un.
func (e QuotaError) Error() string {
	cause := ErrQuotaAtteint.Error()
	if e.Err != nil {
		cause = e.Err.Error()
	}
	if e.Debit.Reprise <= 0 {
		return cause
	}
	return cause + ": reprise annoncee dans " + e.Debit.Reprise.String()
}

// Unwrap rend la cause, pour que errors.Is et errors.As traversent jusqu'au motif du
// fournisseur.
func (e QuotaError) Unwrap() error { return e.Err }

// Is reconnaît la saturation, QUELLE QUE SOIT la cause portée, et même sans aucune cause.
//
// Sans cette méthode, la reconnaissance dépendrait de ce que l'appelant a mis dans Err, et le
// type tiendrait sa promesse une fois sur deux. Un faux de test qui écrirait
// QuotaError{Debit: …} pour simuler une passerelle saturée ne se reconnaîtrait pas comme
// telle, le test passerait pour de mauvaises raisons, et le défaut n'apparaîtrait qu'en
// production, sur la branche qui devait faire patienter.
//
// Ce type NOMME une saturation : il en est une, par construction, et c'est ce que cette
// méthode rend vrai sans condition.
func (e QuotaError) Is(cible error) bool { return cible == ErrQuotaAtteint }

// DelaiAvantRejeu rend le délai que la passerelle a annoncé, et dit s'il y en avait un.
//
// FAUX NE VEUT PAS DIRE « REJOUABLE TOUT DE SUITE », et la confusion coûterait cher : il veut
// dire que la passerelle n'a rien annoncé, et l'appelant garde alors sa décision. Pour un
// palier WhatsApp, qui se compte en destinataires distincts par vingt-quatre heures, l'attente
// utile reste de l'ordre de l'heure.
func DelaiAvantRejeu(err error) (time.Duration, bool) {
	var quota QuotaError
	if !errors.As(err, &quota) || quota.Debit.Reprise <= 0 {
		return 0, false
	}
	return quota.Debit.Reprise, true
}
