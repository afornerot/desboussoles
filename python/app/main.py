"""API FastAPI — questionnaire question par question.

Contrat avec le front :
  1. POST /sessions (corps vide)            → { session_id, ...question "q_comment_ca_va", go_chat, json_patient }
  2. POST /reponse {session_id, id_question, reponse}
        → question suivante                  { session_id, ...question (go_chat compris), json_patient (temporaire) }
        → ou, si complet ou alerte          { "id_question": "fin", ...JSON patient final }
  Question facultative laissée vide : le front envoie "pass" → enregistré dans le JSON patient, question non reposée.
  go_chat (schéma question) : false par défaut ; true dès que la réponse à la question q_switch est "oui".

Lancer : uvicorn app.main:app --reload   puis ouvrir http://localhost:8000/docs
Sessions en mémoire uniquement (expiration 30 min) ; aucun texte utilisateur écrit sur disque.
"""
from __future__ import annotations

import secrets
import time
from dataclasses import dataclass, field
from typing import Literal, Optional, Union

from fastapi import FastAPI, HTTPException, Response
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field

from .decision import SEUIL_ALERTE, Statut, decider
from .fusion import construire, evaluer
from .questions import QUESTIONS, premiere_textarea, question, questions_pour, valider_reponse
from .schema import PASS, Personne, Question

AVERTISSEMENT = "Démonstrateur : n'utilisez que des situations fictives. Aucun diagnostic, aucun conseil de soin."

# Bloc de crise : texte fixe, jamais généré automatiquement (à faire valider par la coordination PTSM).
BLOC_CRISE = [
    "Si vous ou la personne concernée êtes en danger maintenant : appelez le 15 (SAMU) ou le 112.",
    "Pensées suicidaires, ou inquiet pour un proche : 3114, numéro national de prévention du suicide, gratuit, 24h/24, 7j/7.",
    "Violences : 17 (police) — 3919 (violences conjugales, gratuit, 24h/24) — 119 (enfance en danger).",
    "Urgences psychiatriques : CHU de Besançon (urgences adultes) ; pour un mineur, urgences pédiatriques du CHU.",
]
EXPIRATION = 30 * 60


class JSONUTF8(JSONResponse):
    media_type = "application/json; charset=utf-8"  # certains clients (PowerShell 5.1) supposent Latin-1 sinon


app = FastAPI(title="POC hh2026 — orientation santé mentale (Doubs)", version="0.2.0", description=AVERTISSEMENT,
              default_response_class=JSONUTF8)


@app.exception_handler(HTTPException)
async def erreur_http(_, exc: HTTPException):
    """Erreurs : clé `error` (message lisible, lue par le front DesBoussoles) + `detail` (question à reposer, etc.)."""
    d = exc.detail
    message = d.get("erreur", str(d)) if isinstance(d, dict) else str(d)
    return JSONUTF8({"error": message, "detail": d}, status_code=exc.status_code)


@dataclass
class Session:
    id: str
    messages: list[str] = field(default_factory=list)       # réponses textarea (ré-analysées comme saisie libre)
    origines: list[str] = field(default_factory=list)       # id_question de chaque message (origine des mots-clés)
    reponses: dict[str, str] = field(default_factory=dict)  # champ → valeur (ou « pass »)
    suivi: dict[str, str] = field(default_factory=dict)     # id_question → « ok » / « pass »
    go_chat: bool = False                                   # passe à True dès que le front envoie go_chat = « oui »
    maj: float = field(default_factory=time.time)


SESSIONS: dict[str, Session] = {}


def _session(sid: str) -> Session:
    maintenant = time.time()
    for k in [k for k, s in SESSIONS.items() if maintenant - s.maj > EXPIRATION]:
        del SESSIONS[k]
    s = SESSIONS.get(sid)
    if s is None:
        raise HTTPException(404, "session inconnue ou expirée")
    s.maj = maintenant
    return s


# --- modèles échangés avec le front ---
class QuestionEnvoyee(Question):
    """Question à afficher, accompagnée du JSON patient temporaire (état après la dernière réponse).
    `bloc_crise` n'apparaît que si un risque est détecté (texte fixe, à afficher en premier)."""
    session_id: str
    bloc_crise: Optional[list[str]] = None
    json_patient: Optional[Personne] = None


ID_SWITCH = "q_switch"  # question dont la réponse « oui » fait passer go_chat à true


class ReponseFront(BaseModel):
    session_id: str
    id_question: str = Field(examples=["q_comment_ca_va"])
    reponse: str = Field(max_length=5000, examples=["mon fils a 15 ans il fume des joints tous les jours"],
                         description="« pass » (ou vide) pour une question facultative laissée sans réponse")


class PersonneFinale(Personne):
    """JSON patient final, renvoyé quand le questionnaire est terminé (complet, ou alerte si risque.niveau ≥ seuil).
    `bloc_crise` n'est présent que si un risque est détecté."""
    id_question: Literal["fin"] = "fin"
    bloc_crise: Optional[list[str]] = None


def _bloc(p: Personne) -> Optional[list[str]]:
    return BLOC_CRISE if p.risque.niveau >= 2 else None


def _patient(s: Session) -> Personne:
    """JSON patient temporaire de la session (recalculé à partir de tout l'historique)."""
    p = construire(s.id, s.messages, s.reponses, origines=s.origines)
    p.suivi_questions = dict(s.suivi)
    p.go_chat = s.go_chat
    return p


def _question(s: Session | None, q: Question, p: Personne) -> QuestionEnvoyee:
    d = q.model_dump() | {"go_chat": p.go_chat}  # go_chat : true si la réponse à q_switch a été « oui »
    return QuestionEnvoyee(session_id=s.id if s else p.session_id, bloc_crise=_bloc(p), json_patient=p, **d)


def _suite(s: Session) -> Union[QuestionEnvoyee, PersonneFinale]:
    """Complète le JSON patient, puis renvoie la question suivante (avec le JSON temporaire)
    — ou le JSON final si complet / alerte."""
    p = _patient(s)
    restantes = questions_pour(p, set(s.suivi))
    statut, _ = decider(p, restantes)
    if statut == Statut.en_cours:
        return _question(s, restantes[0], p)
    return PersonneFinale(bloc_crise=_bloc(p), **p.model_dump())


def _sortie(obj: BaseModel, code: int = 200) -> JSONUTF8:
    d = obj.model_dump(mode="json")
    if d.get("bloc_crise") is None:
        d.pop("bloc_crise", None)  # n'apparaît que s'il y a un risque
    return JSONUTF8(d, status_code=code)


def _refus(s: Session, q: Question, message: str):
    """Réponse refusée : 422 avec le message et la même question à reposer."""
    raise HTTPException(422, {"erreur": message, "question": _question(s, q, _patient(s)).model_dump(mode="json")})


def _enregistrer(s: Session, q: Question, reponse: str) -> None:
    r = reponse.strip()
    if r == "" or r.lower() in (PASS, "passe"):  # « passe » : envoyé par le bouton « Passer » du front DesBoussoles
        if q.mandatory:
            _refus(s, q, f"la question « {q.id_question} » est obligatoire")
        s.suivi[q.id_question] = PASS
        if q.champ:
            s.reponses[q.champ] = PASS  # renseigné « pass » dans le JSON patient : la question n'est plus reposée
        return
    if q.type == "textarea":  # texte libre : ajouté à l'historique et ré-analysé (mots-clés compris)
        s.messages.append(r)
        s.origines.append(q.id_question)
    elif q.champ is None:  # question sans champ du schéma patient (ex. q_switch)
        valeurs = {o.valeur.lower() for o in q.options}
        if valeurs and r.lower() not in valeurs:
            _refus(s, q, f"valeurs possibles : {[o.valeur for o in q.options]}")
        if q.id_question == ID_SWITCH and r.lower() == "oui":
            s.go_chat = True
    else:
        try:
            s.reponses[q.champ] = valider_reponse(q.champ, r)
        except ValueError as e:
            _refus(s, q, str(e))
    s.suivi[q.id_question] = "ok"


# --- routes ---
@app.get("/", tags=["info"])
def accueil():
    return {"service": app.title, "avertissement": AVERTISSEMENT, "documentation": "/docs",
            "contrat": {"1": "POST /sessions (corps vide) → session_id + question « q_comment_ca_va »",
                        "2": "POST /reponse {session_id, id_question, reponse} → question suivante, "
                             "ou JSON patient final avec id_question = « fin » (complet, ou alerte de niveau ≥ "
                             f"{SEUIL_ALERTE})",
                        "pass": "question facultative sans réponse : envoyer reponse = « pass »",
                        "json_patient": "chaque question est accompagnée du JSON patient temporaire",
                        "go_chat": "false par défaut dans chaque question ; true dès que la réponse à q_switch est « oui »"}}


@app.post("/sessions", response_model=QuestionEnvoyee, status_code=201, tags=["questionnaire"])
def creer_session():
    """Crée une session (corps vide) et renvoie la première question : « Comment ça va ? » (saisie libre),
    avec le JSON patient temporaire (vide)."""
    s = Session(id=secrets.token_hex(8))
    SESSIONS[s.id] = s
    q = premiere_textarea() or QUESTIONS[0]
    return _sortie(_question(s, q, _patient(s)), 201)


@app.post("/reponse", response_model=Union[QuestionEnvoyee, PersonneFinale], tags=["questionnaire"],
          responses={422: {"description": "réponse refusée : {erreur, question} (la même question est à reposer)"},
                     404: {"description": "session inconnue ou expirée"}})
def recevoir_reponse(corps: ReponseFront):
    """Reçoit {session_id, id_question, reponse}, complète le JSON patient et répond par la question suivante
    accompagnée du JSON patient temporaire — ou, si le questionnaire est complet ou en alerte, par le JSON
    patient final (avec "id_question": "fin"). Réponse « oui » à q_switch : go_chat = true dans les questions suivantes."""
    s = _session(corps.session_id)
    q = question(corps.id_question)
    if q is None:
        raise HTTPException(422, {"erreur": f"id_question inconnu : {corps.id_question!r}",
                                  "id_question_possibles": [x.id_question for x in QUESTIONS]})
    _enregistrer(s, q, corps.reponse)
    return _sortie(_suite(s))


# --- outils (débogage / tests) ---
@app.get("/sessions/{sid}", response_model=Personne, tags=["outils"])
def json_patient_courant(sid: str):
    """JSON patient en cours de remplissage (le même que `json_patient` dans les questions)."""
    return _patient(_session(sid))


@app.delete("/sessions/{sid}", status_code=204, tags=["outils"])
def supprimer_session(sid: str):
    _session(sid)
    del SESSIONS[sid]
    return Response(status_code=204)


@app.get("/questions", response_model=list[Question], tags=["outils"])
def catalogue_questions():
    """Toutes les questions, dans l'ordre où elles peuvent être posées (config/questions.json)."""
    return QUESTIONS


@app.post("/personne/evaluer", response_model=Union[QuestionEnvoyee, PersonneFinale], tags=["outils"])
def evaluer_personne(personne: Personne):
    """Fiche patient écrite à la main : renvoie la première question de champ restante, ou le JSON final."""
    p = evaluer(personne)
    restantes = questions_pour(p, set(p.suivi_questions), avec_textarea=False)
    statut, _ = decider(p, restantes)
    if statut == Statut.en_cours:
        return _sortie(_question(None, restantes[0], p))
    return _sortie(PersonneFinale(bloc_crise=_bloc(p), **p.model_dump()))


@app.get("/personne/schema", tags=["outils"])
def schema_personne():
    """JSON Schema du schéma patient."""
    return Personne.model_json_schema()
