"""Section « info_global » envoyée avec chaque question (et avec le JSON final).

Contenu lu dans config/infos.json (modifiable sans toucher au code ; autre fichier via INFOS_JSON) :
chaque section a une condition d'affichage :
  - toujours : affichée dès la première question (ex. numéros d'urgence) ;
  - mineur   : dès que l'âge indique une personne mineure (réponse à q_age ou texte libre) ;
  - tiers    : dès que la personne écrit pour quelqu'un d'autre (réponse « un tiers » à q_pour_qui).
Régénérer le schéma d'édition : python -m app.infos
"""
from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Literal, Optional

from pydantic import BaseModel, ConfigDict, Field, field_validator

from .schema import Personne

CONFIG = Path(__file__).resolve().parent.parent / "config"


class SectionInfo(BaseModel):
    """Bloc d'informations affiché au front."""
    model_config = ConfigDict(extra="forbid")
    id: str = Field(pattern=r"^[a-z0-9_]+$", description="identifiant unique de la section")
    titre: str = Field(min_length=1)
    condition: Literal["toujours", "mineur", "tiers"] = Field(
        exclude=True, description="toujours · mineur (âge < 18) · tiers (la personne écrit pour quelqu'un d'autre)")
    elements: list[str] = Field(min_length=1)


class FichierInfos(BaseModel):
    model_config = ConfigDict(extra="forbid", populate_by_name=True)
    schema_: Optional[str] = Field(default=None, alias="$schema")
    sections: list[SectionInfo]

    @field_validator("sections")
    @classmethod
    def ids_uniques(cls, sections: list[SectionInfo]):
        ids = [s.id for s in sections]
        if doublons := {i for i in ids if ids.count(i) > 1}:
            raise ValueError(f"id de section en double : {sorted(doublons)}")
        return sections


def charger(chemin: str | Path | None = None) -> list[SectionInfo]:
    chemin = Path(chemin or os.environ.get("INFOS_JSON") or CONFIG / "infos.json")
    try:
        brut = json.loads(chemin.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        raise ValueError(f"{chemin} : JSON invalide ligne {e.lineno}, colonne {e.colno} : {e.msg}") from e
    try:
        return FichierInfos.model_validate(brut).sections
    except Exception as e:
        raise ValueError(f"{chemin} : {e}") from e


SECTIONS: list[SectionInfo] = charger()


def info_global(p: Personne) -> list[SectionInfo]:
    """Sections à afficher selon l'état du JSON patient, dans l'ordre du fichier."""
    conditions = {
        "toujours": True,
        "mineur": p.est_mineur(),
        "tiers": p.demandeur.sur and p.demandeur.value in ("proche", "professionnel"),
    }
    return [s for s in SECTIONS if conditions[s.condition]]


if __name__ == "__main__":  # python -m app.infos : régénère config/infos.schema.json
    schema = FichierInfos.model_json_schema(by_alias=True, mode="validation")
    schema["title"] = "Section info_global (l'ordre de la liste = l'ordre d'affichage)"
    (CONFIG / "infos.schema.json").write_text(json.dumps(schema, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"écrit : {CONFIG / 'infos.schema.json'}")
    charger()
    print("config/infos.json valide")
