backend: uvicorn backend.main:app --host ${API_HOST:-127.0.0.1} --port ${API_PORT:-8000}
frontend: streamlit run frontend/app.py --server.port ${STREAMLIT_PORT:-8501} --server.address ${API_HOST:-127.0.0.1} --server.headless true
