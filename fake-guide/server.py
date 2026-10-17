"""Fake API Guide — simule le questionnaire pour les tests de Boussole.

Endpoints:
  POST /sessions -> premiere question {session_id, id_question, champ, texte, type, options?, mandatory}
  POST /reponse  -> question suivante, ou {session_id, id_question: "fin"} a la fin
"""
import json
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer

QUESTIONS = [
    {
        "id_question": "q1",
        "champ": "humeur",
        "texte": "Comment te sens-tu aujourd'hui ?",
        "type": "choice",
        "mandatory": True,
        "options": [
            {"valeur": "bien", "libelle": "Plutôt bien"},
            {"valeur": "moyen", "libelle": "Moyen"},
            {"valeur": "mal", "libelle": "Plutôt mal"},
        ],
    },
    {
        "id_question": "q2",
        "champ": "duree",
        "texte": "Depuis quand est-ce que ça dure ?",
        "type": "choice",
        "mandatory": False,
        "options": [
            {"valeur": "jours", "libelle": "Quelques jours"},
            {"valeur": "semaines", "libelle": "Quelques semaines"},
            {"valeur": "mois", "libelle": "Plusieurs mois"},
        ],
    },
    {
        "id_question": "q3",
        "champ": "souci",
        "texte": "En quelques mots, qu'est-ce qui te préoccupe le plus ?",
        "type": "texte",
        "mandatory": True,
    },
    {
        "id_question": "q4",
        "champ": "commune",
        "texte": "Dans quelle commune vis-tu ?",
        "type": "commune",
        "mandatory": True,
    },
    {
        "id_question": "q5",
        "champ": "complement",
        "texte": "Y a-t-il autre chose que tu veux partager ?",
        "type": "textarea",
        "mandatory": False,
    },
]

SESSIONS = {}  # session_id -> index de prochaine question


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        if n == 0:
            return {}
        try:
            return json.loads(self.rfile.read(n))
        except Exception:
            return {}

    def do_POST(self):
        if self.path.rstrip("/") == "/sessions":
            sid = uuid.uuid4().hex
            SESSIONS[sid] = 0
            q = dict(QUESTIONS[0])
            q["session_id"] = sid
            self._send(200, q)
            return

        if self.path.rstrip("/") == "/reponse":
            data = self._body()
            sid = data.get("session_id", "")
            if sid not in SESSIONS:
                self._send(404, {"error": "session inconnue"})
                return
            print(f"[reponse] sid={sid[:8]} question={data.get('id_question')} reponse={data.get('reponse')!r}", flush=True)
            SESSIONS[sid] += 1
            i = SESSIONS[sid]
            if i >= len(QUESTIONS):
                self._send(200, {"session_id": sid, "id_question": "fin"})
                return
            q = dict(QUESTIONS[i])
            q["session_id"] = sid
            self._send(200, q)
            return

        self._send(404, {"error": "not found"})

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    print("fake guide api on :8000", flush=True)
    HTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
