"""Construction du schéma `Personne` à partir de l'historique de messages et des réponses de l'utilisateur."""
from __future__ import annotations

from datetime import date

from . import extracteurs as ex
from . import motscles
from .gazetteer import SECTEUR_BESANCON_ADULTE, ResultatCommune, gazetteer
from .normalisation import fold, tokens
from .normalisation import texte as normaliser
from .schema import PASS, MotCle, Personne, Source, champ, tranche_pour_age

R, G, L, U, D = Source.regex, Source.gazetteer, Source.lexique, Source.utilisateur, Source.derive


def construire(session_id: str, messages: list[str], reponses: dict[str, str] | None = None,
               aujourdhui: date | None = None, origines: list[str] | None = None) -> Personne:
    """Ré-extrait TOUT l'historique à chaque tour, puis applique les réponses de l'utilisateur (prioritaires).
    `origines` : id_question de chaque message (pour l'origine des mots-clés)."""
    reponses = reponses or {}
    txt = normaliser("\n".join(messages))
    gaz = gazetteer()
    p = Personne(session_id=session_id)
    pc, b = p.personne, p.besoin

    # Demandeur
    d = ex.extraire_demandeur(txt)
    if d.valeur:
        p.demandeur = champ(d.valeur, d.confidence, R, d.evidence)
    if reponses.get("demandeur") == "tiers":  # « Pour un tiers » : professionnel si le texte l'indique, sinon proche
        p.demandeur = champ("professionnel" if d.valeur == "professionnel" else "proche", 1, U, d.evidence)
    elif "demandeur" in reponses:
        p.demandeur = champ(reponses["demandeur"], 1, U)

    # Âge : rattaché à la personne concernée selon le demandeur
    a = ex.extraire_age(txt, aujourdhui)
    if (choix := ex.choisir_age(a, p.demandeur.connu)) is not None:
        cand, conf = choix
        pc.age_ans = champ(cand.age, conf, R, cand.evidence)
    if a.tranche and not (a.tranche == "0-2" and p.demandeur.value == "soi"):  # « mon bébé » écrit par la mère
        pc.tranche_age = champ(a.tranche, a.tranche_conf, R, a.tranche_evidence)
    if "age" in reponses:
        v = reponses["age"]
        if v == PASS:
            pc.age_ans = champ(PASS, 1, U)
        elif v.isdigit():
            pc.age_ans = champ(int(v), 1, U)
        else:
            pc.age_ans, pc.tranche_age = type(pc.age_ans)(), champ(v, 1, U)
    if pc.age_ans.connu is not None:
        pc.tranche_age = champ(tranche_pour_age(pc.age_ans.connu), pc.age_ans.confidence, D)

    # Commune / quartier
    c = c_texte = gaz.extraire(txt)
    if reponses.get("commune") == PASS:
        pc.commune_nom, c = champ(PASS, 1, U), ResultatCommune()
    elif "commune" in reponses:
        c = gaz.resoudre(reponses["commune"])
        c.confidence = 1.0 if c.confidence >= 0.8 else c.confidence
        if not c.trouve:  # commune hors du Doubs saisie par l'utilisateur : hors périmètre du guide
            c = ResultatCommune(hors_doubs=reponses["commune"].strip(), confidence=1.0, evidence=reponses["commune"])
        elif c.commune is not None and c.commune is c_texte.commune and not c.quartier and c_texte.quartier:
            c.quartier = c_texte.quartier  # Besançon : quartier cité dans le texte libre, conservé
    if c.trouve:
        src = U if "commune" in reponses else G
        pc.commune_nom = champ(c.nom, c.confidence, src, c.evidence)
        if c.commune and c.commune.get("insee"):
            pc.commune_insee = champ(c.commune["insee"], c.confidence, src)
        pc.hors_perimetre_guide = champ(c.hors_perimetre, c.confidence, D)
        if c.quartier:
            pc.quartier_besancon = champ(c.quartier["nom"], c.confidence, src, c.evidence)
    if reponses.get("quartier") == PASS:
        pc.quartier_besancon = champ(PASS, 1, U)
    elif "quartier" in reponses:
        q = gaz.resoudre(reponses["quartier"])
        if q.quartier:
            pc.quartier_besancon = champ(q.quartier["nom"], 1, U)
            if not pc.commune_nom.rempli:
                pc.commune_nom = champ("Besançon", 1, U)
                pc.commune_insee = champ("25056", 1, U)
                pc.hors_perimetre_guide = champ(False, 1, D)

    # Lexique : statuts, parcours, type de demande, suivi
    lx = ex.analyser_lexique(txt)
    for s, extraits in lx.statuts:
        if (s == "parent" and p.demandeur.value != "soi") or (s == "professionnel_sante" and p.demandeur.value == "professionnel"):
            continue  # le statut concerne la personne concernée, pas celle qui écrit
        pc.statuts.append(champ(s, 0.6, L, extraits[0]))
    for parcours, extraits in lx.parcours:
        if not b.parcours_principal.rempli and parcours != "urgence":
            b.parcours_principal = champ(parcours, min(0.6 + 0.1 * (len(extraits) - 1), 0.8), L, ", ".join(extraits))
        elif parcours != b.parcours_principal.value and parcours not in b.parcours_secondaires:
            b.parcours_secondaires.append(parcours)
    if lx.deja_suivi is not None:
        b.deja_suivi = champ(lx.deja_suivi, 0.7, L, lx.suivi_extrait)
    # Réponses aux questions de durée, d'impact et d'accompagnement (valeur ou « pass »)
    if "deja_suivi" in reponses:
        v = reponses["deja_suivi"]
        b.deja_suivi = champ(PASS if v == PASS else v == "oui", 1, U)
    for cle_champ in ("duree", "impact_domaine", "impact_niveau"):
        if cle_champ in reponses:
            setattr(b, cle_champ, champ(reponses[cle_champ], 1, U))

    # Risque : composant de sécurité, indépendant de tout LLM
    niveau, dets, negation = ex.detecter_crise(txt)
    p.risque.niveau, p.risque.negation_detectee = niveau, negation
    p.risque.indices = [d.extrait for d in dets]
    p.risque.sources = [L] if dets else []

    # Parcours sans indice mais signal de crise
    if not b.parcours_principal.rempli and niveau >= 2:
        b.parcours_principal = champ("urgence" if niveau >= 3 else "humeur_depression", 0.6, D)
    if "parcours" in reponses:
        ancien = b.parcours_principal.value  # repéré dans le texte : conservé comme besoin secondaire
        if ancien and ancien != reponses["parcours"] and ancien not in b.parcours_secondaires:
            b.parcours_secondaires.insert(0, ancien)
        b.parcours_principal = champ(reponses["parcours"], 1, U)
        b.parcours_secondaires = [x for x in b.parcours_secondaires if x != reponses["parcours"]]

    # Type de demande
    if niveau >= 3:
        b.type_demande = champ("urgence", 0.8, D)
    elif p.demandeur.value == "proche":
        b.type_demande = champ("aide_pour_un_proche", 0.6, D)
    elif lx.type_demande:
        t, extraits = lx.type_demande[0]
        b.type_demande = champ(t, 0.6, L, extraits[0])

    p.mots_cles = _mots_cles(messages, origines, lx, pc.statuts, dets, c_texte, d.relation)
    _deriver(p, gaz)
    p.calculer_manquants(ordre_questions())
    return p


def _mots_cles(messages, origines, lx, statuts, dets, commune, relation) -> list[MotCle]:
    """Mots importants de chaque message libre, annotés de ce qu'ils ont permis de repérer."""
    reperes: dict[str, list[str]] = {}

    def noter(extrait: str | None, rep: str) -> None:
        for w in motscles.mots_de(extrait or ""):
            if rep not in reperes.setdefault(w, []):
                reperes[w].append(rep)

    for parcours, extraits in lx.parcours:
        for x in extraits:
            noter(x, f"parcours:{parcours}")
    for s in statuts:  # seulement les statuts retenus pour la personne concernée
        noter(s.evidence, f"statut:{s.value}")
    for det in dets:
        noter(det.extrait, f"crise:{det.retenu}")
    if commune.trouve:
        noter(commune.evidence, "commune")
    if relation:
        noter(relation, "demandeur")

    out, vus = [], set()
    for i, message in enumerate(messages):
        origine = origines[i] if origines and i < len(origines) else None
        for mot in motscles.extraire(message):
            f = fold(mot)
            if f in vus:
                continue
            vus.add(f)
            reps = []
            for w in tokens(f):
                reps += [r for r in reperes.get(w, []) if r not in reps]
            out.append(MotCle(mot=mot, origine=origine, reperes=reps))
    return out


def evaluer(p: Personne) -> Personne:
    """Fiche patient fournie telle quelle (ex. JSON écrit à la main) : complète les champs dérivés
    (tranche d'âge, commune officielle, secteurs) puis recalcule les champs manquants."""
    p = p.model_copy(deep=True)
    gaz = gazetteer()
    pc = p.personne
    if pc.age_ans.connu is not None and (not pc.tranche_age.rempli or pc.tranche_age.source == D):
        pc.tranche_age = champ(tranche_pour_age(pc.age_ans.connu), pc.age_ans.confidence, D)
    if pc.commune_nom.connu:
        r = gaz.resoudre(pc.commune_nom.value)
        if r.trouve:
            conf = min(pc.commune_nom.confidence, r.confidence) if r.confidence < 0.6 else pc.commune_nom.confidence
            pc.commune_nom = champ(r.nom, conf, pc.commune_nom.source or U, pc.commune_nom.evidence)
            if r.commune and r.commune.get("insee"):
                pc.commune_insee = champ(r.commune["insee"], conf, pc.commune_nom.source)
            pc.hors_perimetre_guide = champ(r.hors_perimetre, conf, D)
    pc.secteur_adulte, pc.secteur_infanto_juvenile = type(pc.secteur_adulte)(), type(pc.secteur_adulte)()
    _deriver(p, gaz)
    p.calculer_manquants(ordre_questions())
    return p


def ordre_questions() -> list[str]:
    from .questions import QUESTIONS  # import local : questions dépend du gazetteer, pas de la fusion
    return [q.champ for q in QUESTIONS if q.champ]


def _deriver(p: Personne, gaz) -> None:
    """Secteurs (adulte / infanto-juvénile) et parcours liés à l'âge."""
    pc, b = p.personne, p.besoin
    if pc.commune_nom.connu and not pc.hors_perimetre_guide.value:
        c, conf = gaz.par_nom(pc.commune_nom.connu), pc.commune_nom.confidence
        if c:
            if not p.est_mineur() and c.get("secteur_adulte"):
                pc.secteur_adulte = champ(c["secteur_adulte"], conf, D)  # Besançon : BESANCON_A_PRECISER (selon la rue)
            if not p.est_majeur():
                sij = c.get("secteur_ij") or ""
                if p.est_besancon():  # mineur à Besançon : secteur selon le quartier
                    sij = next((q["secteur_ij"] for q in gaz.quartiers if q["nom"] == pc.quartier_besancon.value), "")
                if sij:
                    pc.secteur_infanto_juvenile = champ(sij, conf, D)
    if p.est_mineur() and b.parcours_principal.value != "enfant_adolescent":
        if not b.parcours_principal.rempli:
            b.parcours_principal = champ("enfant_adolescent", 0.6, D)
        elif "enfant_adolescent" not in b.parcours_secondaires:
            b.parcours_secondaires.append("enfant_adolescent")
    if (pc.age_ans.connu is not None and pc.age_ans.connu >= 65) and "personnes_agees" not in b.parcours_secondaires \
            and b.parcours_principal.value != "personnes_agees":
        b.parcours_secondaires.append("personnes_agees")


__all__ = ["construire", "SECTEUR_BESANCON_ADULTE"]
