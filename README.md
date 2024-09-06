### OLLAMAUI COMPANION APP

A simple app acting as backend for Ollamaui. It provides the following features :
* request queue acting as intermediate between ollama and ollamaui. 
* storage of chat logs
* summarization of the chat logs (in essence : 'memories')
* optionally, access to a SearxNg instance.

#### Requirements
* a maria or mysql server, and a database user with CREATE and GRANT privileges *(root for example)*
* a small LLM to be used to summarize chat logs fast without clogging your precious memory *(recommendation : **qwen2:0.5b**)*
* *(optional but highly recommended)* a SearxNg instance **capable of returning json**

#### Installation
Run install.sh to install the dependencies and setup the database. 

#### Useage
run ./restart.sh to update, build and start the server.
