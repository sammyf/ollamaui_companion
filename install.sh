#!/bin/bash
echo Installing the Ollamaui Companion App.
echo
echo It implements a basic queuing mechanism for requests,
echo a memory management system and a
echo search engine query system for your LLMs
echo.
echo installing GO dependencies
go get -u github.com/google/uuid
go get -u github.com/go-sql-driver/mysql

# Prompt the user for input
read -p "Enter server IP: " server_ip
read -p "Enter database user: " db_user
read -s -p "Enter database password: " db_password
read -p "Which summarizer should be used if no model is loaded: " summarizer
read -p "URL to your SearxNG instance ? (NOTE : It MUST be able to serve json!): " searx
echo

echo "
# Export the variables with the user input
export DEBUG=0
export DB_USER=\"$db_user\"
export DB_PASSWORD=\"$db_password\"
export DB_HOST=\"$server_ip\"
export DB_NAME=ollamaui
export SUMMARIZER=\"$summarizer\"
export SEARX=\"$searx\"$$$$
./m
" > run.sh

echo importing the DB Schema
echo For this a database user with create and grant privilege is needed!
cp ollamaui-user.sql.dist ollamaui-user.sql
sed -i 's/{{DBUSER}}/$db_user/g' ollamaui-user.sql
sed -i 's/{{DBPASSWORD}}/$db_password/g' ollamaui-user.sql
read -p "Enter privileged database user: " root_user
read -s -p "Enter password (press return if no password is needed!): " root_password
if [ "$root_password" = "" ]; then
  mysql -u $root_user < ollamaui-scheme.sql
  mysql -u $root_user < ollamaui-user.sql
else;
  mysql -u $root_user -p$root_password < ollamaui-scheme.sql
  mysql -u $root_user -p$root_password < ollamaui-user.sql
fi

go build
echo "Installation done."
echo "Use ./restart.sh to update and restart the companion."

