@echo off
rem Lance l'API FastAPI sur http://localhost:8000 — documentation interactive : http://localhost:8000/docs
chcp 65001 >nul
cd /d "%~dp0"
if not exist .venv (
  python -m venv .venv
  .venv\Scripts\python.exe -m pip install -r requirements-dev.txt
)
start "" http://localhost:8000/docs
rem --reload-include : le serveur redémarre aussi quand config\questions.json est modifié
.venv\Scripts\python.exe -m uvicorn app.main:app --reload --reload-include "*.json" --port 8000
pause
