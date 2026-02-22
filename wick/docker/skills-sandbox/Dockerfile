FROM python:3.11-slim

# Common data-science and utility dependencies for skill scripts
RUN pip install --no-cache-dir \
    pandas \
    numpy \
    scipy \
    matplotlib \
    openpyxl \
    requests \
    python-pptx

RUN mkdir -p /workspace /skills /scripts

# Bake skills into image as fallback (dev uses volume mount)
COPY skills/ /skills/

# Bake converter scripts into image
COPY scripts/ /scripts/

WORKDIR /workspace

# Keep container alive as a daemon
CMD ["sleep", "infinity"]
