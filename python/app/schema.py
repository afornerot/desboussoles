"""Schéma `Personne` (éléments de base du CLAUDE.md, §4).

Chaque champ extrait est un `Champ` : valeur + confiance + source + extrait déclencheur.
Une valeur absente vaut `None` (sérialisée `null`).
"""
from __future__ import annotations

from enum import Enum
from typing import Generic, Literal, Optional, TypeVar, Union

from pydantic import BaseModel, Field

T = TypeVar("T")

SEUIL_CONFIRME = 0.6  # en dessous : champ « à confirmer », il reste dans champs_manquants


class Source(str, Enum):
    regex = "regex"
    gazetteer = "gazetteer"
    lexique = "lexique"
    utilisateur = "utilisateur"
    derive = "derive"


PASS = "pass"  # question facultative laissée sans réponse : le champ est marqué pour ne pas être redemandé


class Champ(BaseModel, Generic[T]):
    value: Optional[Union[T, Literal["pass"]]] = None
    confidence: float = 0.0
    source: Optional[Source] = None
    evidence: Optional[str] = None  # extrait du message (texte normalisé)

    @property
    def rempli(self) -> bool:  # renseigné, y compris « pass »
        return self.value is not None

    @property
    def sur(self) -> bool:  # renseigné avec assez de confiance (« pass » compte : on ne redemande pas)
        return self.value is not None and self.confidence >= SEUIL_CONFIRME

    @property
    def connu(self):  # valeur exploitable pour les calculs (None si absente ou « pass »)
        return None if self.value in (None, PASS) else self.value


def champ(value, confidence: float, source: Source, evidence: str | None = None) -> Champ:
    return Champ(value=value, confidence=round(confidence, 2), source=source, evidence=evidence or None)


# --- Énumérations (listes fermées) ---
DEMANDEURS = ["soi", "proche", "professionnel"]
TRANCHES_AGE = ["0-2", "3-6", "7-12", "13-17", "18-25", "26-64", "65+"]
PARCOURS = [
    "ecoute_soutien", "urgence", "enfant_adolescent", "perinatalite", "adulte_general",
    "humeur_depression", "addiction", "soins_ambulatoires", "precarite", "victimologie_vif",
    "specifique", "personnes_agees", "social", "hebergement", "somatique", "retablissement", "inconnu",
]
STATUTS = [
    "agriculteur", "entrepreneur", "professionnel_sante", "etudiant", "eleve", "parent",
    "femme_enceinte", "sans_domicile", "migrant", "victime_violences", "aidant", "retraite", "sans_emploi",
]
TYPES_DEMANDE = ["parler", "information", "soin", "urgence", "hebergement", "aide_sociale",
                 "aide_pour_un_proche", "inconnu"]
DUREES = ["moins_15_jours", "moins_1_mois", "moins_6_mois", "plus_6_mois"]
IMPACT_DOMAINES = ["travail", "etudes", "logement", "famille", "relations_sociales"]
IMPACT_NIVEAUX = ["peu", "moderement", "beaucoup"]


def tranche_pour_age(age: int) -> str:
    for borne, t in ((2, "0-2"), (6, "3-6"), (12, "7-12"), (17, "13-17"), (25, "18-25"), (64, "26-64")):
        if age <= borne:
            return t
    return "65+"


def _desc(texte: str, valeurs: list[str] | None = None) -> dict:
    d = {"description": texte + (f" — valeurs : {', '.join(valeurs)}" if valeurs else "")}
    return d


class MotCle(BaseModel):
    """Mot important écrit par la personne, qu'il figure ou non dans un lexique."""
    mot: str
    origine: Optional[str] = Field(default=None, description="id_question de la réponse d'où vient le mot (ex. q_comment_ca_va)")
    reperes: list[str] = Field(default_factory=list,
                               description="ce que le mot a permis de repérer : parcours:…, statut:…, crise:N, commune, demandeur")


class Risque(BaseModel):
    niveau: int = Field(0, ge=0, le=3, description="0 aucun indice · 1 détresse · 2 idées/auto-agression · 3 plan, moyen, danger immédiat")
    indices: list[str] = Field(default_factory=list, description="extraits du message qui justifient le niveau")
    negation_detectee: bool = False
    sources: list[Source] = Field(default_factory=list)


class PersonneConcernee(BaseModel):
    age_ans: Champ[int] = Field(default_factory=Champ, **_desc("âge de la personne concernée (0-110)"))
    tranche_age: Champ[str] = Field(default_factory=Champ, **_desc("tranche d'âge", TRANCHES_AGE))
    commune_insee: Champ[str] = Field(default_factory=Champ, **_desc("code INSEE de la commune (ex. 25056 Besançon)"))
    commune_nom: Champ[str] = Field(default_factory=Champ, **_desc("nom officiel de la commune"))
    quartier_besancon: Champ[str] = Field(default_factory=Champ, **_desc("quartier, si Besançon (requis pour un mineur)"))
    secteur_adulte: Champ[str] = Field(default_factory=Champ, **_desc("dérivé : pôle de psychiatrie adulte"))
    secteur_infanto_juvenile: Champ[str] = Field(default_factory=Champ, **_desc("dérivé : CGI de secteur"))
    hors_perimetre_guide: Champ[bool] = Field(default_factory=Champ, **_desc("commune hors du périmètre du guide"))
    statuts: list[Champ[str]] = Field(default_factory=list, **_desc("statuts de la personne concernée", STATUTS))


class Besoin(BaseModel):
    parcours_principal: Champ[str] = Field(default_factory=Champ, **_desc("besoin principal", PARCOURS))
    parcours_secondaires: list[str] = Field(default_factory=list, **_desc("autres besoins", PARCOURS))
    type_demande: Champ[str] = Field(default_factory=Champ, **_desc("ce que la personne attend", TYPES_DEMANDE))
    deja_suivi: Champ[bool] = Field(default_factory=Champ,
                                    **_desc("déjà un accompagnement, ou déjà parlé à son médecin (question q_accompagnement)"))
    duree: Champ[str] = Field(default_factory=Champ, **_desc("depuis combien de temps dure la difficulté", DUREES))
    impact_domaine: Champ[str] = Field(default_factory=Champ, **_desc("domaine de vie touché", IMPACT_DOMAINES))
    impact_niveau: Champ[str] = Field(default_factory=Champ, **_desc("niveau d'impact", IMPACT_NIVEAUX))


class Personne(BaseModel):
    """Schéma patient. Les champs requis sont age, commune, demandeur, parcours (+ quartier pour un mineur à
    Besançon) ; un champ dont la confiance est < 0.6 reste « à confirmer ». L'ordre des questions posées pour
    les champs manquants est celui de config/questions.json."""
    session_id: str = ""
    demandeur: Champ[str] = Field(default_factory=Champ, **_desc("qui écrit", DEMANDEURS))
    personne: PersonneConcernee = Field(default_factory=PersonneConcernee)
    besoin: Besoin = Field(default_factory=Besoin)
    risque: Risque = Field(default_factory=Risque)
    mots_cles: list[MotCle] = Field(default_factory=list, description="mots importants des réponses libres (début et fin)")
    champs_manquants: list[str] = Field(default_factory=list, description="calculé : champs requis manquants, dans l'ordre des questions")
    suivi_questions: dict[str, str] = Field(default_factory=dict,
                                            description="id_question → « ok » (répondue) ou « pass » (facultative laissée vide)")
    # --- aides ---
    def est_mineur(self) -> bool:
        p = self.personne
        if p.age_ans.connu is not None:
            return p.age_ans.connu < 18
        return p.tranche_age.connu in ("0-2", "3-6", "7-12", "13-17")

    def est_majeur(self) -> bool:
        p = self.personne
        if p.age_ans.connu is not None:
            return p.age_ans.connu >= 18
        return p.tranche_age.connu in ("18-25", "26-64", "65+")

    def est_besancon(self) -> bool:
        return self.personne.commune_nom.value == "Besançon"

    def calculer_manquants(self, ordre: list[str] | None = None) -> None:
        """Champs requis manquants, triés selon `ordre` (ordre de config/questions.json) ;
        par défaut : âge → commune → demandeur → parcours → quartier."""
        p, m = self.personne, []
        if not (p.age_ans.sur or p.tranche_age.sur):
            m.append("age")
        hors = p.hors_perimetre_guide.sur and p.hors_perimetre_guide.value
        commune_ok = hors or p.commune_nom.sur
        if not commune_ok:
            m.append("commune")
        if not self.demandeur.sur:
            m.append("demandeur")
        pp = self.besoin.parcours_principal
        if not (pp.sur and pp.value != "inconnu"):
            m.append("parcours")
        if commune_ok and not hors and self.est_mineur() and self.est_besancon() and not p.quartier_besancon.sur:
            m.append("quartier")
        if ordre:
            m.sort(key=lambda c: ordre.index(c) if c in ordre else len(ordre))
        self.champs_manquants = m

    @property
    def pret(self) -> bool:
        return not self.champs_manquants


class Option(BaseModel):
    valeur: str
    libelle: str


TypeQuestion = Literal["choice", "texte", "commune", "adresse", "textarea"]


class Question(BaseModel):
    id_question: str  # identifiant unique (sert à répondre : POST /reponse)
    # choice : boutons · texte : saisie courte · commune : commune ou code postal · adresse : adresse complète
    # (la commune en est extraite) · textarea : texte libre analysé
    type: TypeQuestion
    champ: Optional[str] = None  # champ du schéma patient rempli ; aucun pour textarea
    texte: str  # texte affiché (version « tiers » si la personne écrit pour quelqu'un d'autre)
    mandatory: bool  # obligatoire : une réponse vide est refusée ; sinon la question peut être passée
    options: list[Option] = Field(default_factory=list)
    # configuration interne (non envoyée au front)
    texte_tiers: Optional[str] = Field(default=None, exclude=True)
    toujours: bool = Field(default=False, exclude=True)


if __name__ == "__main__":  # python -m app.schema : écrit config/personne.schema.json
    import json
    from pathlib import Path

    s = Personne.model_json_schema()
    s["title"] = "Schéma patient (Personne)"
    s["properties"]["$schema"] = {"type": "string"}  # permet "$schema" dans vos fichiers patient
    sortie = Path(__file__).resolve().parent.parent / "config" / "personne.schema.json"
    sortie.write_text(json.dumps(s, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"écrit : {sortie}")
