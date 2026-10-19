package ai

import "testing"

func TestIsSommaire(t *testing.T) {
	sommaire := "[3.1.4 Maison des Adolescents [29](#mda)](#mda)\n\n[3.1.5 PAEJ [30](#paej)](#paej)\n\n[3.2 Autre [31](#autre)](#autre)"
	if !isSommaire(sommaire) {
		t.Fatal("le sommaire doit être filtré")
	}
	fiche := "### Maison des Adolescents (MDA-25) CHN\n\n**COORDONNEES**\n\n13 Rue des Jardins, 25000 Besançon\n\nTél. 03 81 53 97 67"
	if isSommaire(fiche) {
		t.Fatal("une fiche ne doit pas être filtrée")
	}
}
