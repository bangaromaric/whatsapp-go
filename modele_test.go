package whatsapp

import (
	"strings"
	"testing"
)

// TestNouveauModeleRefuseCeQueLaPlateformeRefuse couvre les gardes qui existent précisément
// parce que le refus de Meta, lui, n'explique rien.
//
// Chaque cas de ce tableau correspond à un message rejeté en production avec une erreur qui
// ne nomme ni la variable, ni la raison. Les attraper ici les transforme en échec immédiat,
// avant la moindre requête, et avec le numéro de la variable coupable.
func TestNouveauModeleRefuseCeQueLaPlateformeRefuse(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		modele    string
		variables []string
	}{
		{nom: "nom de template vide", modele: "", variables: []string{"123456"}},
		{nom: "nom de template en blancs", modele: "   ", variables: []string{"123456"}},
		{nom: "variable vide", modele: "code_auth", variables: []string{""}},
		{nom: "variable en blancs", modele: "code_auth", variables: []string{"  "}},
		{nom: "saut de ligne", modele: "code_auth", variables: []string{"123\n456"}},
		{nom: "retour chariot", modele: "code_auth", variables: []string{"123\r456"}},
		{nom: "tabulation", modele: "code_auth", variables: []string{"123\t456"}},
		{nom: "variable trop longue", modele: "code_auth", variables: []string{strings.Repeat("a", 121)}},
		{nom: "seconde variable fautive", modele: "code_auth", variables: []string{"ok", "mauvais\n"}},
		// encoding/json ne refuse pas ces octets, il les remplace par des losanges sans
		// rien dire : sans cette garde, le message partirait, facturé, avec un losange à
		// la place du caractère. C'est pour cela que le contrôle est ici et non dans
		// l'adaptateur, qui ne verrait rien d'anormal.
		{nom: "utf-8 invalide", modele: "code_auth", variables: []string{"A\xffB"}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			got, err := NouveauModele(c.modele, c.variables...)
			if err == nil {
				t.Errorf("NouveauModele(%q, %q) = %+v, nil, want une erreur", c.modele, c.variables, got)
			}
		})
	}
}

// TestNouveauModeleAccepteCeQuiTient vérifie que les gardes ne débordent pas.
func TestNouveauModeleAccepteCeQuiTient(t *testing.T) {
	t.Parallel()

	cas := []struct {
		nom       string
		variables []string
	}{
		{nom: "aucune variable", variables: nil},
		{nom: "une variable", variables: []string{"123456"}},
		{nom: "accents et espaces", variables: []string{"Boutique Élégance"}},
		{nom: "pile la longueur maximale", variables: []string{strings.Repeat("é", longueurMaxVariable)}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			t.Parallel()

			if _, err := NouveauModele("code_auth", c.variables...); err != nil {
				t.Errorf("NouveauModele(code_auth, %q) = _, %v, want nil", c.variables, err)
			}
		})
	}
}

// TestLaLongueurSeCompteEnCaracteresEtNonEnOctets fige le choix de l'unité.
//
// Le plafond de la plateforme porte sur des caractères. Le compter en octets refuserait un
// nom de boutique accentué de soixante lettres, parce que chaque accent pèse deux octets en
// UTF-8, et le refus serait incompréhensible pour qui compte avec ses yeux.
func TestLaLongueurSeCompteEnCaracteresEtNonEnOctets(t *testing.T) {
	t.Parallel()

	// Cent vingt é font deux cent quarante octets et cent vingt caractères.
	variable := strings.Repeat("é", longueurMaxVariable)
	if len(variable) <= longueurMaxVariable {
		t.Fatalf("len(variable) = %d, want > %d: le cas de test ne prouve rien", len(variable), longueurMaxVariable)
	}

	if _, err := NouveauModele("code_auth", variable); err != nil {
		t.Errorf("NouveauModele avec %d caracteres = _, %v, want nil", longueurMaxVariable, err)
	}
}

// TestLesVariablesNeSontPasPartageables vérifie qu'un modèle est bien une valeur.
//
// Sans copie, un adaptateur qui trierait ou réécrirait la tranche rendue changerait le modèle
// de son appelant, et deux envois du même modèle ne porteraient pas le même message.
func TestLesVariablesNeSontPasPartageables(t *testing.T) {
	t.Parallel()

	entree := []string{"123456"}
	m, err := NouveauModele("code_auth", entree...)
	if err != nil {
		t.Fatalf("NouveauModele() = _, %v, want nil", err)
	}

	entree[0] = "000000"
	m.Variables()[0] = "999999"

	if got, want := m.Variables()[0], "123456"; got != want {
		t.Errorf("Variables()[0] = %q, want %q", got, want)
	}
}

// TestLaReferenceRefusePlutotQueDeTronquer fige la décision la plus importante du type.
//
// Une référence tronquée reste lisible dans un journal mais ne retrouve plus rien chez le
// fournisseur : la réconciliation d'un message perdu devient impossible, et au pire moment,
// celui où quelqu'un affirme n'avoir jamais reçu son message. L'implémentation de référence
// tronque en silence ; refuser est le comportement voulu.
func TestLaReferenceRefusePlutotQueDeTronquer(t *testing.T) {
	t.Parallel()

	trop := strings.Repeat("r", longueurMaxReference+1)
	if got, err := NouvelleReference(trop); err == nil {
		t.Errorf("NouvelleReference(%d octets) = %q, nil, want une erreur", len(trop), got)
	}

	pile := strings.Repeat("r", longueurMaxReference)
	got, err := NouvelleReference(pile)
	if err != nil {
		t.Fatalf("NouvelleReference(%d octets) = _, %v, want nil", len(pile), err)
	}
	if got.String() != pile {
		t.Errorf("String() = %d caracteres, want la valeur intacte de %d", len(got.String()), len(pile))
	}
}

// TestLaReferenceRefuseLeVide vérifie qu'un 2xx sans identifiant ne passe pas pour un succès.
func TestLaReferenceRefuseLeVide(t *testing.T) {
	t.Parallel()

	for _, vide := range []string{"", "   "} {
		if _, err := NouvelleReference(vide); err == nil {
			t.Errorf("NouvelleReference(%q) = _, nil, want une erreur", vide)
		}
	}
}
